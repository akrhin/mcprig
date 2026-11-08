package registry

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/sintez/mcprig/internal/atomicfile"
	"github.com/sintez/mcprig/internal/config"
)

// Store is the manager registry. Every exported method takes the lock, so the
// HTTP layer may call it from concurrent handlers.
type Store struct {
	mu   sync.Mutex
	dir  string
	path string
	data Data
}

// Open reads <dir>/registry.json. A missing file starts an empty registry; an
// unparsable one is renamed to registry.json.corrupt-<unix> and replaced with
// an empty one. An empty registry is filled from MANAGER_SEED_FILE when set.
func Open(dir string) (*Store, error) {
	s := &Store{dir: dir, path: filepath.Join(dir, RegistryFile)}
	s.data = Data{Version: 1, NextPort: FirstPort}

	raw, readErr := os.ReadFile(s.path)
	switch {
	case readErr == nil:
		var data Data
		if uerr := json.Unmarshal(raw, &data); uerr != nil {
			backup := fmt.Sprintf("%s.corrupt-%d", s.path, time.Now().Unix())
			if rerr := os.Rename(s.path, backup); rerr != nil {
				return nil, fmt.Errorf("реестр %s не разобран (%v), отложить не удалось: %w", s.path, uerr, rerr)
			}
		} else {
			s.data = data
			s.normalize()
			// Persist normalization once, so a migrated legacy key keeps its
			// createdAt instead of being rebuilt on every start.
			canon, merr := marshalData(s.data)
			if merr != nil {
				return nil, merr
			}
			if !bytes.Equal(bytes.TrimSpace(raw), bytes.TrimSpace(canon)) {
				if serr := s.saveLocked(); serr != nil {
					return nil, serr
				}
			}
		}
	case errors.Is(readErr, fs.ErrNotExist):
		// Fresh registry, no file yet.
	default:
		return nil, readErr
	}

	if len(s.data.Servers) == 0 && len(s.data.Sets) == 0 {
		if seed := os.Getenv("MANAGER_SEED_FILE"); seed != "" {
			body, err := os.ReadFile(seed)
			if err != nil {
				return nil, fmt.Errorf("сид %s: %w", seed, err)
			}
			if err := s.applySeed(body); err != nil {
				return nil, fmt.Errorf("сид %s: %w", seed, err)
			}
			if err := s.saveLocked(); err != nil {
				return nil, err
			}
		}
	}
	return s, nil
}

// normalize repairs the invariants of a loaded file; it is called before the
// store is shared, so no locking is needed.
func (s *Store) normalize() {
	if s.data.Version == 0 {
		s.data.Version = 1
	}
	if s.data.NextPort < FirstPort || s.data.NextPort > LastPort {
		s.data.NextPort = FirstPort
	}
	servers := make([]Server, 0, len(s.data.Servers))
	for _, srv := range s.data.Servers {
		if srv == nil {
			continue
		}
		if srv.Transport == "" {
			if srv.URL != "" {
				srv.Transport = config.TransportHTTP
			} else {
				srv.Transport = config.TransportStdio
			}
		}
		servers = append(servers, srv)
	}
	s.data.Servers = servers

	now := time.Now().UTC().Format(time.RFC3339)
	sets := make([]*Set, 0, len(s.data.Sets))
	for _, set := range s.data.Sets {
		if set == nil {
			continue
		}
		if set.Name == "" {
			set.Name = set.ID
		}
		set.Keys = normalizeKeys(set.Keys, set.Token, now)
		set.Token = activeToken(set.Keys)
		sets = append(sets, set)
	}
	s.data.Sets = sets
}

// applySeed fills an empty registry from the token-free seed format.
func (s *Store) applySeed(body []byte) error {
	var seed struct {
		Servers []json.RawMessage `json:"servers"`
		Sets    []json.RawMessage `json:"sets"`
	}
	if err := json.Unmarshal(body, &seed); err != nil {
		return fmt.Errorf("не разобран: %w", err)
	}
	now := time.Now().UTC().Format(time.RFC3339)
	for _, raw := range seed.Servers {
		in, err := decodeObject(raw)
		if err != nil {
			return fmt.Errorf("сервер: %w", err)
		}
		name, err := requiredString(in, "name")
		if err != nil {
			return fmt.Errorf("сервер: %w", err)
		}
		if s.serverLocked(name) != nil {
			continue
		}
		srv, err := parseServer(name, in)
		if err != nil {
			return fmt.Errorf("сервер %s: %w", name, err)
		}
		s.data.Servers = append(s.data.Servers, srv)
	}
	for _, raw := range seed.Sets {
		in, err := decodeObject(raw)
		if err != nil {
			return fmt.Errorf("набор: %w", err)
		}
		set, err := s.buildSet(nil, in)
		if err != nil {
			return err
		}
		if s.setLocked(set.ID) != nil {
			continue
		}
		if !set.Open {
			// The seed carries no tokens: issue a fresh one, except for an
			// open set, which runs without a key.
			set.Keys = []Key{newKey(now)}
			set.Token = set.Keys[0].Token
		}
		s.data.Sets = append(s.data.Sets, set)
	}
	return nil
}

func marshalData(data Data) ([]byte, error) {
	body, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(body, '\n'), nil
}

// Save writes the registry atomically.
func (s *Store) Save() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.saveLocked()
}

// saveLocked writes the live state; used where the state is already final
// (Open normalization, seeding, explicit Save).
func (s *Store) saveLocked() error {
	body, err := marshalData(s.data)
	if err != nil {
		return err
	}
	return s.writeLocked(body)
}

// commitLocked persists next and only then makes it the live state: a failed
// write leaves memory as it was, never ahead of disk.
func (s *Store) commitLocked(next Data) error {
	body, err := marshalData(next)
	if err != nil {
		return err
	}
	if err := s.writeLocked(body); err != nil {
		return err
	}
	s.data = next
	return nil
}

// writeLocked writes bytes atomically, keeps the previous version in
// registry.json.bak and never leaves a partial registry.json behind.
func (s *Store) writeLocked(body []byte) error {
	if old, err := os.ReadFile(s.path); err == nil {
		if err := atomicfile.Write(s.path+".bak", old); err != nil {
			return fmt.Errorf("резервная копия реестра: %w", err)
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return atomicfile.Write(s.path, body)
}

// clone copies the slice headers, so a mutation of the copy cannot reach the
// live state. The entries themselves are never mutated in place.
func (d Data) clone() Data {
	d.Servers = append([]Server(nil), d.Servers...)
	d.Sets = append([]*Set(nil), d.Sets...)
	return d
}

// Servers returns a copy of the server list.
func (s *Store) Servers() []Server {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Server(nil), s.data.Servers...)
}

// Server returns the named server, or nil.
func (s *Store) Server(name string) Server {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.serverLocked(name)
}

func (s *Store) serverLocked(name string) Server {
	if i := s.serverIndexLocked(name); i >= 0 {
		return s.data.Servers[i]
	}
	return nil
}

func (s *Store) serverIndexLocked(name string) int {
	for i, srv := range s.data.Servers {
		if srv.Name == name {
			return i
		}
	}
	return -1
}

// removeAt drops element i and copies the rest, so the result never aliases in.
func removeAt[T any](in []T, i int) []T {
	out := make([]T, 0, len(in)-1)
	out = append(out, in[:i]...)
	return append(out, in[i+1:]...)
}

// AddServer validates and stores one server entry given as a JSON object.
func (s *Store) AddServer(raw json.RawMessage) (Server, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	in, err := decodeObject(raw)
	if err != nil {
		return nil, err
	}
	name, err := requiredString(in, "name")
	if err != nil {
		return nil, err
	}
	if s.serverLocked(name) != nil {
		return nil, fmt.Errorf("сервер %s уже есть", name)
	}
	srv, err := parseServer(name, in)
	if err != nil {
		return nil, err
	}
	next := s.data.clone()
	next.Servers = append(next.Servers, srv)
	if err := s.commitLocked(next); err != nil {
		return nil, err
	}
	return srv, nil
}

// UpdateServer applies a partial patch; the name never changes.
func (s *Store) UpdateServer(name string, patch json.RawMessage) (Server, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cur := s.serverLocked(name)
	if cur == nil {
		return nil, fmt.Errorf("нет сервера %s", name)
	}
	in, err := decodeObject(patch)
	if err != nil {
		return nil, err
	}
	merged, err := mergeServer(cur, in)
	if err != nil {
		return nil, err
	}
	srv, err := parseServer(name, merged)
	if err != nil {
		return nil, err
	}
	next := s.data.clone()
	next.Servers[s.serverIndexLocked(name)] = srv
	if err := s.commitLocked(next); err != nil {
		return nil, err
	}
	return srv, nil
}

// DeleteServer removes a server and every reference to it from the sets.
func (s *Store) DeleteServer(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	idx := s.serverIndexLocked(name)
	if idx < 0 {
		return fmt.Errorf("нет сервера %s", name)
	}
	next := s.data.clone()
	next.Servers = removeAt(next.Servers, idx)
	for i, set := range next.Sets {
		kept := make([]string, 0, len(set.Servers))
		for _, serverName := range set.Servers {
			if serverName != name {
				kept = append(kept, serverName)
			}
		}
		if len(kept) == len(set.Servers) {
			continue
		}
		cp := *set
		cp.Servers = kept
		next.Sets[i] = &cp
	}
	return s.commitLocked(next)
}

// Sets returns a snapshot of the set list; callers never get live pointers.
func (s *Store) Sets() []*Set {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]*Set, 0, len(s.data.Sets))
	for _, set := range s.data.Sets {
		out = append(out, set.snapshot())
	}
	return out
}

// Set returns a snapshot of the set with this id, or nil.
func (s *Store) Set(id string) *Set {
	s.mu.Lock()
	defer s.mu.Unlock()
	if set := s.setLocked(id); set != nil {
		return set.snapshot()
	}
	return nil
}

// snapshot copies the mutable parts of a set, so a caller cannot write back
// into registry state through the returned pointer.
func (set *Set) snapshot() *Set {
	cp := *set
	cp.Servers = append([]string(nil), set.Servers...)
	cp.Keys = append([]Key(nil), set.Keys...)
	return &cp
}

func (s *Store) setLocked(id string) *Set {
	if i := s.setIndexLocked(id); i >= 0 {
		return s.data.Sets[i]
	}
	return nil
}

func (s *Store) setIndexLocked(id string) int {
	for i, set := range s.data.Sets {
		if set.ID == id {
			return i
		}
	}
	return -1
}

// AddSet validates and stores one set, allocating a port and an initial key
// unless the set is open.
func (s *Store) AddSet(raw json.RawMessage) (*Set, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	in, err := decodeObject(raw)
	if err != nil {
		return nil, err
	}
	set, err := s.buildSet(nil, in)
	if err != nil {
		return nil, err
	}
	if s.setLocked(set.ID) != nil {
		return nil, fmt.Errorf("набор %s уже есть", set.ID)
	}
	if !set.Open {
		set.Keys = []Key{newKey(time.Now().UTC().Format(time.RFC3339))}
		set.Token = set.Keys[0].Token
	}
	next := s.data.clone()
	next.Sets = append(next.Sets, set)
	next.NextPort = nextPort(set.Port)
	if err := s.commitLocked(next); err != nil {
		return nil, err
	}
	return set, nil
}

// UpdateSet applies a partial patch; the id never changes and keys are managed
// only through IssueKey and RevokeKey.
func (s *Store) UpdateSet(id string, patch json.RawMessage) (*Set, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	idx := s.setIndexLocked(id)
	if idx < 0 {
		return nil, fmt.Errorf("нет набора %s", id)
	}
	in, err := decodeObject(patch)
	if err != nil {
		return nil, err
	}
	set, err := s.buildSet(s.data.Sets[idx], in)
	if err != nil {
		return nil, err
	}
	next := s.data.clone()
	next.Sets[idx] = set
	if err := s.commitLocked(next); err != nil {
		return nil, err
	}
	return set, nil
}

// DeleteSet removes a set.
func (s *Store) DeleteSet(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	idx := s.setIndexLocked(id)
	if idx < 0 {
		return fmt.Errorf("нет набора %s", id)
	}
	next := s.data.clone()
	next.Sets = removeAt(next.Sets, idx)
	return s.commitLocked(next)
}

// IssueKey revokes the current active key and returns the new plaintext token.
func (s *Store) IssueKey(setID string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	set := s.setLocked(setID)
	if set == nil {
		return "", fmt.Errorf("нет набора %s", setID)
	}
	now := time.Now().UTC().Format(time.RFC3339)
	keys := make([]Key, len(set.Keys), len(set.Keys)+1)
	copy(keys, set.Keys)
	for i := range keys {
		if keys[i].RevokedAt == "" {
			keys[i].RevokedAt = now
		}
	}
	key := newKey(now)
	keys = append(keys, key)
	cp := *set
	cp.Keys = keys
	cp.Token = key.Token
	next := s.data.clone()
	next.Sets[s.setIndexLocked(setID)] = &cp
	if err := s.commitLocked(next); err != nil {
		return "", err
	}
	return key.Token, nil
}

// RevokeKey revokes one key. wasActive reports whether it was the active one,
// which leaves the set without a token.
func (s *Store) RevokeKey(setID, keyID string) (wasActive bool, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	set := s.setLocked(setID)
	if set == nil {
		return false, fmt.Errorf("нет набора %s", setID)
	}
	idx := -1
	for i, key := range set.Keys {
		if key.ID == keyID {
			idx = i
			break
		}
	}
	if idx < 0 {
		return false, fmt.Errorf("нет ключа %s у набора %s", keyID, setID)
	}
	keys := make([]Key, len(set.Keys))
	copy(keys, set.Keys)
	if wasActive = keys[idx].RevokedAt == ""; wasActive {
		keys[idx].RevokedAt = time.Now().UTC().Format(time.RFC3339)
	}
	cp := *set
	cp.Keys = keys
	cp.Token = activeToken(keys)
	next := s.data.clone()
	next.Sets[s.setIndexLocked(setID)] = &cp
	if err := s.commitLocked(next); err != nil {
		return wasActive, err
	}
	return wasActive, nil
}

// AllocatePort reserves the next free port in 9100–9199.
func (s *Store) AllocatePort() (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	port, err := s.allocPortLocked()
	if err != nil {
		return 0, err
	}
	next := s.data.clone()
	next.NextPort = nextPort(port)
	if err := s.commitLocked(next); err != nil {
		return 0, err
	}
	return port, nil
}

// allocPortLocked picks a free port without changing the live state.
func (s *Store) allocPortLocked() (int, error) {
	used := make(map[int]bool, len(s.data.Sets))
	for _, set := range s.data.Sets {
		used[set.Port] = true
	}
	start := s.data.NextPort
	if start < FirstPort || start > LastPort {
		start = FirstPort
	}
	for port := start; port <= LastPort; port++ {
		if !used[port] {
			return port, nil
		}
	}
	for port := FirstPort; port < start; port++ {
		if !used[port] {
			return port, nil
		}
	}
	return 0, fmt.Errorf("свободных портов в диапазоне %d–%d нет", FirstPort, LastPort)
}

func nextPort(port int) int {
	if port >= LastPort {
		return FirstPort
	}
	return port + 1
}

// PublicSet is the token-free view of a set for HTTP clients.
func PublicSet(set *Set) PublicSetEntry {
	out := PublicSetEntry{
		ID:          set.ID,
		Name:        set.Name,
		Description: set.Description,
		Servers:     append([]string(nil), set.Servers...),
		Port:        set.Port,
		KeyPrefix:   keyPrefix(set.Keys),
		Autostart:   set.Autostart,
		Open:        set.Open,
	}
	for _, key := range set.Keys {
		out.Keys = append(out.Keys, PublicKey{
			ID:        key.ID,
			CreatedAt: key.CreatedAt,
			RevokedAt: key.RevokedAt,
			Note:      key.Note,
		})
	}
	return out
}

// PublicSets is PublicSet over the whole list.
func (s *Store) PublicSets() []PublicSetEntry {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]PublicSetEntry, 0, len(s.data.Sets))
	for _, set := range s.data.Sets {
		out = append(out, PublicSet(set))
	}
	return out
}
