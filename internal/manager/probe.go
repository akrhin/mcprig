package manager

import (
	"context"
	"time"

	"github.com/sintez/mcprig/internal/downstream"
	"github.com/sintez/mcprig/internal/registry"
)

const probeTimeout = 60 * time.Second

type probeResult struct {
	OK    bool     `json:"ok"`
	Tools []string `json:"tools"`
	Error string   `json:"error,omitempty"`
	MS    int64    `json:"ms"`
}

// probeServer — initialize + tools/list, бюджет 60 с. Ошибка всегда в теле, не
// в статусе: probe не валит API.
func (m *Manager) probeServer(ctx context.Context, srv registry.Server) probeResult {
	start := time.Now()
	out := probeResult{Tools: []string{}}
	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()

	if msg := srv.Check(); msg != "" {
		out.Error = msg
		out.MS = time.Since(start).Milliseconds()
		return out
	}
	session, err := (&downstream.Connector{Version: m.version, Env: managerEnv(m.sec)}).Connect(ctx, expandServer(srv, m.sec))
	if err != nil {
		out.Error = truncate(err.Error(), 800)
		out.MS = time.Since(start).Milliseconds()
		return out
	}
	defer session.Close()

	tools, err := session.ListTools(ctx, nil)
	if err != nil {
		out.Error = truncate(err.Error(), 800)
		out.MS = time.Since(start).Milliseconds()
		return out
	}
	for _, t := range tools.Tools {
		out.Tools = append(out.Tools, t.Name)
	}
	out.OK = true
	out.MS = time.Since(start).Milliseconds()
	return out
}
