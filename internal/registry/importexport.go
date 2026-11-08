package registry

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
)

// Skip is one import entry that did not make it in, with the reason.
type Skip struct {
	Name   string `json:"name"`
	Reason string `json:"reason"`
}

// Import adds servers from a decoded MCPHub/lazy-mcp `mcpServers` object. Each
// value is a command string, an argv array or a server object. A bad entry is
// reported in skipped and does not stop the rest; existing names are skipped,
// not overwritten. A failed save is reported as a Skip with an empty name.
func (s *Store) Import(entries map[string]json.RawMessage) ([]string, []Skip) {
	s.mu.Lock()
	defer s.mu.Unlock()

	names := make([]string, 0, len(entries))
	for name := range entries {
		names = append(names, name)
	}
	sort.Strings(names)

	next := s.data.clone()
	var added []string
	var skipped []Skip
	for _, name := range names {
		if s.serverLocked(name) != nil {
			skipped = append(skipped, Skip{Name: name, Reason: "сервер уже есть в реестре"})
			continue
		}
		in, err := importObject(entries[name])
		if err != nil {
			skipped = append(skipped, Skip{Name: name, Reason: err.Error()})
			continue
		}
		srv, err := parseServer(name, in)
		if err != nil {
			skipped = append(skipped, Skip{Name: name, Reason: err.Error()})
			continue
		}
		next.Servers = append(next.Servers, srv)
		added = append(added, name)
	}
	if len(added) > 0 {
		if err := s.commitLocked(next); err != nil {
			return nil, append(skipped, Skip{Reason: err.Error()})
		}
	}
	return added, skipped
}

// importObject turns one import value into a server body: an argv array or an
// object as is. A bare string used to be split into argv, but a path with spaces
// broke silently; it is refused instead.
func importObject(raw json.RawMessage) (map[string]json.RawMessage, error) {
	trimmed := bytes.TrimSpace(raw)
	if isNull(trimmed) {
		return nil, errors.New("запись не объект")
	}
	switch trimmed[0] {
	case '{':
		return decodeObject(trimmed)
	case '"':
		return nil, errors.New("command: передайте массив")
	case '[':
		var argv []string
		if err := json.Unmarshal(trimmed, &argv); err != nil {
			return nil, errors.New("command: ожидался массив строк")
		}
		encoded, err := json.Marshal(argv)
		if err != nil {
			return nil, err
		}
		return map[string]json.RawMessage{"command": encoded}, nil
	default:
		return nil, errors.New("запись не объект")
	}
}

// ExportSet renders one set as `{"mcpServers":{…}}`, keeping `${VAR}`
// references in env and headers unexpanded.
func (s *Store) ExportSet(id string) (map[string]any, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	set := s.setLocked(id)
	if set == nil {
		return nil, fmt.Errorf("нет набора %s", id)
	}
	servers := make(map[string]any, len(set.Servers))
	for _, name := range set.Servers {
		srv := s.serverLocked(name)
		if srv == nil {
			continue
		}
		servers[name] = exportServer(srv)
	}
	return map[string]any{"mcpServers": servers}, nil
}

func exportServer(srv Server) map[string]any {
	out := map[string]any{"enabled": srv.Enabled}
	if len(srv.Command) > 0 {
		out["command"] = srv.Command
	}
	if srv.URL != "" {
		out["url"] = srv.URL
	}
	if len(srv.Env) > 0 {
		out["env"] = srv.Env
	}
	if len(srv.Headers) > 0 {
		out["headers"] = srv.Headers
	}
	if srv.Description != "" {
		out["description"] = srv.Description
	}
	if srv.Cwd != "" {
		out["cwd"] = srv.Cwd
	}
	if len(srv.Tags) > 0 {
		out["tags"] = srv.Tags
	}
	return out
}
