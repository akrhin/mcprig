// Package config reads the lazy-mcp `servers.json` format. The format is a
// contract with the sets this binary spawns, so it stays byte-compatible with
// what lazy-mcp already accepts.
package config

import (
	"encoding/json"
	"fmt"
	"os"
)

type TransportKind string

const (
	TransportStdio TransportKind = "stdio"
	TransportHTTP  TransportKind = "http"
)

// Server is one downstream MCP server. Validation never fails the load: an
// unusable entry keeps its ConfigError and shows up unhealthy, the rest works.
type Server struct {
	Name        string            `json:"name"`
	Description string            `json:"description,omitempty"`
	Transport   TransportKind     `json:"transport,omitempty"`
	Command     []string          `json:"command,omitempty"`
	Cwd         string            `json:"cwd,omitempty"`
	Env         map[string]string `json:"env,omitempty"`
	URL         string            `json:"url,omitempty"`
	Headers     map[string]string `json:"headers,omitempty"`
	Tags        []string          `json:"tags,omitempty"`
	Enabled     bool              `json:"enabled,omitempty"`
	ConfigError string            `json:"_configError,omitempty"`
}

// UnmarshalJSON accepts both command forms: the array form and the legacy
// `"command": "npx"` plus `"args": […]`.
func (s *Server) UnmarshalJSON(data []byte) error {
	type raw struct {
		Name        string          `json:"name"`
		Description string          `json:"description"`
		Transport   string          `json:"transport"`
		Command     json.RawMessage `json:"command"`
		Args        []string        `json:"args"`
		Cwd         string          `json:"cwd"`
		Env         map[string]any  `json:"env"`
		URL         string          `json:"url"`
		Headers     map[string]any  `json:"headers"`
		Tags        []string        `json:"tags"`
		Enabled     *bool           `json:"enabled"`
	}
	var r raw
	if err := json.Unmarshal(data, &r); err != nil {
		return err
	}
	s.Name, s.Description, s.Cwd, s.URL, s.Tags = r.Name, r.Description, r.Cwd, r.URL, r.Tags
	s.Transport = TransportKind(r.Transport)
	s.Enabled = r.Enabled == nil || *r.Enabled
	if len(r.Command) > 0 {
		var argv []string
		if err := json.Unmarshal(r.Command, &argv); err == nil {
			s.Command = argv
		} else {
			var one string
			if err := json.Unmarshal(r.Command, &one); err != nil {
				return fmt.Errorf("command: ожидалась строка или массив строк")
			}
			s.Command = append([]string{one}, r.Args...)
		}
	}
	s.Env = stringMap(r.Env, &s.ConfigError, "env")
	s.Headers = stringMap(r.Headers, &s.ConfigError, "headers")
	return nil
}

func stringMap(in map[string]any, configErr *string, field string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		str, ok := v.(string)
		if !ok {
			if *configErr == "" {
				*configErr = fmt.Sprintf("%s.%s: ожидалась строка", field, k)
			}
			continue
		}
		out[k] = str
	}
	return out
}

// validate fills ConfigError. Returns false when the entry is unusable.
func (s *Server) validate() bool {
	if s.ConfigError != "" {
		return false
	}
	if s.Name == "" {
		s.ConfigError = "нет имени"
		return false
	}
	switch s.Transport {
	case "":
		switch {
		case s.URL != "":
			s.Transport = TransportHTTP
		case len(s.Command) > 0:
			s.Transport = TransportStdio
		default:
			s.ConfigError = "не удалось определить тип: ни url, ни command"
			return false
		}
	case TransportStdio:
		if len(s.Command) == 0 {
			s.ConfigError = "stdio требует command"
			return false
		}
		if s.URL != "" {
			s.ConfigError = "stdio не может иметь url"
			return false
		}
	case TransportHTTP:
		if s.URL == "" {
			s.ConfigError = "http требует url"
			return false
		}
		if len(s.Command) > 0 {
			s.ConfigError = "http не может иметь command"
			return false
		}
	default:
		s.ConfigError = fmt.Sprintf("неизвестный transport %q", s.Transport)
		return false
	}
	if s.Cwd != "" && s.Transport != TransportStdio {
		s.ConfigError = "cwd допустим только для stdio"
		return false
	}
	return true
}

// HTTPTransport is the listener the set exposes to MCP clients.
type HTTPTransport struct {
	Type         string   `json:"type,omitempty"`
	Host         string   `json:"host,omitempty"`
	Port         int      `json:"port,omitempty"`
	Path         string   `json:"path,omitempty"`
	AuthToken    string   `json:"authToken,omitempty"`
	AllowedHosts []string `json:"allowedHosts,omitempty"`
}

type HealthMonitor struct {
	Interval int `json:"interval,omitempty"` // ms
	Timeout  int `json:"timeout,omitempty"`  // ms
}

// Config is one set's file: the servers it proxies plus its listener.
type Config struct {
	Servers        []*Server      `json:"servers"`
	Transport      *HTTPTransport `json:"transport,omitempty"`
	HealthMonitor  *HealthMonitor `json:"healthMonitor,omitempty"`
	RequestTimeout int            `json:"requestTimeout,omitempty"` // ms
}

// Load reads a config file. A server that cannot be identified (no name) fails
// the whole load; every other defect is soft-failed onto that server.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var shape struct {
		Servers        []json.RawMessage `json:"servers"`
		Transport      *HTTPTransport    `json:"transport"`
		HealthMonitor  *HealthMonitor    `json:"healthMonitor"`
		RequestTimeout int               `json:"requestTimeout"`
	}
	if err := json.Unmarshal(data, &shape); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	cfg := &Config{Transport: shape.Transport, HealthMonitor: shape.HealthMonitor, RequestTimeout: shape.RequestTimeout}
	seen := make(map[string]bool, len(shape.Servers))
	for i, raw := range shape.Servers {
		srv := &Server{}
		if err := json.Unmarshal(raw, srv); err != nil {
			return nil, fmt.Errorf("%s: сервер #%d: %w", path, i+1, err)
		}
		if srv.Name == "" {
			return nil, fmt.Errorf("%s: сервер #%d без имени", path, i+1)
		}
		if seen[srv.Name] {
			srv.ConfigError = "дубль имени"
		}
		seen[srv.Name] = true
		srv.validate()
		cfg.Servers = append(cfg.Servers, srv)
	}
	return cfg, nil
}

// Usable reports whether the set can be served: at least one healthy server.
func (c *Config) Usable() []*Server {
	out := make([]*Server, 0, len(c.Servers))
	for _, s := range c.Servers {
		if s.ConfigError == "" && s.Enabled {
			out = append(out, s)
		}
	}
	return out
}
