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

	"github.com/sintez/mcp-manager/internal/config"
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

// saveLocked writes tmp + fsync + rename, keeps the previous version in
// registry.json.bak and never leaves a partial registry.json behind.
func (s *Store) saveLocked() error {
	body, err := marshalData(s.data)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return err
	}
	if old, err := os.ReadFile(s.path); err == nil {
		if err := writeFile(s.path+".bak", old); err != nil {
			return fmt.Errorf("резервная копия реестра: %w", err)
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return err
	}

	tmp, err := os.CreateTemp(s.dir, RegistryFile+".tmp-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	if _, err := tmp.Write(body); err != nil {
		tmp.Close()
		os.Remove(name)
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		os.Remove(name)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(name)
		return err
	}
	if err := os.Rename(name, s.path); err != nil {
		os.Remove(name)
		return err
	}
	if d, err := os.Open(s.dir); err == nil {
		_ = d.Sync()
		_ = d.Close()
	}
	return nil
}

func writeFile(name string, body []byte) error {
	f, err := os.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.Write(body); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	return f.Close()
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
	for _, srv := range s.data.Servers {
		if srv.Name == name {
			return srv
		}
	}
	return nil
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
	s.data.Servers = append(s.data.Servers, srv)
	if err := s.saveLocked(); err != nil {
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
	for i, old := range s.data.Servers {
		if old.Name == name {
			s.data.Servers[i] = srv
			break
		}
	}
	if err := s.saveLocked(); err != nil {
		return nil, err
	}
	return srv, nil
}

// DeleteServer removes a server and every reference to it from the sets.
func (s *Store) DeleteServer(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	idx := -1
	for i, srv := range s.data.Servers {
		if srv.Name == name {
			idx = i
			break
		}
	}
	if idx < 0 {
		return fmt.Errorf("нет сервера %s", name)
	}
	s.data.Servers = append(append([]Server(nil), s.data.Servers[:idx]...), s.data.Servers[idx+1:]...)
	for i, set := range s.data.Sets {
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
		s.data.Sets[i] = &cp
	}
	return s.saveLocked()
}

// Sets returns a copy of the set list.
func (s *Store) Sets() []*Set {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]*Set(nil), s.data.Sets...)
}

// Set returns the set with this id, or nil.
func (s *Store) Set(id string) *Set {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.setLocked(id)
}

func (s *Store) setLocked(id string) *Set {
	for _, set := range s.data.Sets {
		if set.ID == id {
			return set
		}
	}
	return nil
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
	s.data.Sets = append(s.data.Sets, set)
	if err := s.saveLocked(); err != nil {
		return nil, err
	}
	return set, nil
}

// UpdateSet applies a partial patch; the id never changes and keys are managed
// only through IssueKey and RevokeKey.
func (s *Store) UpdateSet(id string, patch json.RawMessage) (*Set, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	idx := -1
	for i, set := range s.data.Sets {
		if set.ID == id {
			idx = i
			break
		}
	}
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
	s.data.Sets[idx] = set
	if err := s.saveLocked(); err != nil {
		return nil, err
	}
	return set, nil
}

// DeleteSet removes a set.
func (s *Store) DeleteSet(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, set := range s.data.Sets {
		if set.ID == id {
			s.data.Sets = append(append([]*Set(nil), s.data.Sets[:i]...), s.data.Sets[i+1:]...)
			return s.saveLocked()
		}
	}
	return fmt.Errorf("нет набора %s", id)
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
	s.replaceSet(set, &cp)
	if err := s.saveLocked(); err != nil {
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
	s.replaceSet(set, &cp)
	if err := s.saveLocked(); err != nil {
		return wasActive, err
	}
	return wasActive, nil
}

// replaceSet swaps a published set for an updated copy: a reader holding the
// old pointer keeps reading a consistent snapshot.
func (s *Store) replaceSet(old, next *Set) {
	for i, set := range s.data.Sets {
		if set == old {
			s.data.Sets[i] = next
			return
		}
	}
}

// AllocatePort reserves the next free port in 9100–9199.
func (s *Store) AllocatePort() (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	port, err := s.allocPortLocked()
	if err != nil {
		return 0, err
	}
	if err := s.saveLocked(); err != nil {
		return 0, err
	}
	return port, nil
}

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
			s.data.NextPort = nextPort(port)
			return port, nil
		}
	}
	for port := FirstPort; port < start; port++ {
		if !used[port] {
			s.data.NextPort = nextPort(port)
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
