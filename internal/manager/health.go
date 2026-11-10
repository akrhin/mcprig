package manager

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	healthTTL = 15 * time.Second
	// healthTimeout — такой же, как healthTimeout в internal/mcpserver: сервер,
	// который поднимается дольше (stdio-обёртка npx, холодный LSP), иначе
	// выглядит мёртвым, хотя вызовы проходят. Числа обязаны совпадать.
	healthTimeout = 15 * time.Second
	// healthLoadTimeout — бюджет самого запроса: набор тратит до healthTimeout
	// на пробу и ещё немного на ответ, поэтому внешний ровно тот же 15 с
	// истекал одновременно с внутренним и панель показывала «не ответил».
	healthLoadTimeout = 2 * healthTimeout
)

type serverHealth struct {
	Name    string `json:"name"`
	Healthy *bool  `json:"healthy"`
	Error   string `json:"error,omitempty"`
	// Isolation повторяет пометку list_servers: «без изоляции: …» видно и в
	// health, а не только в логе набора.
	Isolation string `json:"isolation,omitempty"`
}

type setHealth struct {
	Running bool           `json:"running"`
	Servers []serverHealth `json:"servers"`
}

type healthEntry struct {
	at   time.Time
	view setHealth
	wait chan struct{}
}

// healthCache — 15 с кэш и single-flight на набор.
type healthCache struct {
	mu      sync.Mutex
	entries map[string]*healthEntry
	// last — последнее известное состояние серверов набора: журнал пишет
	// только смену healthy → unhealthy, а не каждый цикл опроса.
	last map[string]map[string]bool
}

func newHealthCache() *healthCache {
	return &healthCache{entries: map[string]*healthEntry{}, last: map[string]map[string]bool{}}
}

func (c *healthCache) clear(id string) {
	c.mu.Lock()
	delete(c.entries, id)
	delete(c.last, id)
	c.mu.Unlock()
}

func (c *healthCache) get(ctx context.Context, id string, load func(context.Context) setHealth) setHealth {
	for {
		c.mu.Lock()
		e := c.entries[id]
		if e == nil {
			e = &healthEntry{}
			c.entries[id] = e
		}
		if e.view.Servers != nil && time.Since(e.at) < healthTTL {
			v := e.view
			c.mu.Unlock()
			return v
		}
		if e.wait != nil {
			wait := e.wait
			c.mu.Unlock()
			select {
			case <-wait:
				continue
			case <-ctx.Done():
				return setHealth{}
			}
		}
		wait := make(chan struct{})
		e.wait = wait
		c.mu.Unlock()

		// Лидер грузит под своим ctx: отмена запроса клиента не должна
		// попадать в кэш как «context canceled» на весь healthTTL.
		loadCtx, cancel := context.WithTimeout(context.Background(), healthLoadTimeout)
		view := load(loadCtx)
		cancel()

		c.mu.Lock()
		e.view, e.at, e.wait = view, time.Now(), nil
		c.mu.Unlock()
		close(wait)
		return view
	}
}

// bearerTransport добавляет токен набора к каждому запросу клиента MCP.
type bearerTransport struct {
	token string
	base  http.RoundTripper
}

func (t *bearerTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	clone := r.Clone(r.Context())
	if t.token != "" {
		clone.Header.Set("Authorization", "Bearer "+t.token)
	}
	base := t.base
	if base == nil {
		base = http.DefaultTransport
	}
	return base.RoundTrip(clone)
}

// fetchSetHealth спрашивает list_servers у самого набора. Сетевая ошибка —
// healthy:null: это «неизвестно», а не «сломан».
func fetchSetHealth(ctx context.Context, host string, port int, token string, open bool, names []string, version string) setHealth {
	out := setHealth{Running: true, Servers: make([]serverHealth, len(names))}
	for i, name := range names {
		out.Servers[i] = serverHealth{Name: name}
	}
	if token == "" && !open {
		errText := "у набора нет активного ключа"
		for i := range out.Servers {
			out.Servers[i].Error = errText
		}
		return out
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "mcprig", Version: version}, nil)
	transport := &mcp.StreamableClientTransport{
		Endpoint:             "http://" + net.JoinHostPort(host, strconv.Itoa(port)) + "/mcp",
		HTTPClient:           &http.Client{Transport: &bearerTransport{token: token}, Timeout: healthLoadTimeout},
		DisableStandaloneSSE: true,
	}
	// Бюджет запроса — healthLoadTimeout, а не healthTimeout: неразогретый набор
	// тратит до healthTimeout на пробу каждого сервера, и внешний таймаут,
	// равный внутреннему, истекал одновременно с ним — панель показывала
	// «context deadline exceeded» у всех серверов, хотя часть уже поднялась.
	ctx, cancel := context.WithTimeout(ctx, healthLoadTimeout)
	defer cancel()
	session, err := client.Connect(ctx, transport, nil)
	if err != nil {
		return healthError(out, err)
	}
	defer session.Close()

	res, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "list_servers"})
	if err != nil {
		return healthError(out, err)
	}
	var payload struct {
		Servers []struct {
			Name      string `json:"name"`
			Healthy   bool   `json:"healthy"`
			Error     string `json:"error"`
			Isolation string `json:"isolation"`
		} `json:"servers"`
	}
	if text := firstText(res); text != "" {
		if err := json.Unmarshal([]byte(text), &payload); err != nil {
			return healthError(out, err)
		}
	}
	known := map[string]struct {
		healthy   bool
		err       string
		isolation string
	}{}
	for _, s := range payload.Servers {
		known[s.Name] = struct {
			healthy   bool
			err       string
			isolation string
		}{s.Healthy, truncate(s.Error, 500), s.Isolation}
	}
	for i := range out.Servers {
		if s, ok := known[out.Servers[i].Name]; ok {
			healthy := s.healthy
			out.Servers[i].Healthy = &healthy
			out.Servers[i].Error = s.err
			out.Servers[i].Isolation = s.isolation
			continue
		}
		errText := fmt.Sprintf("набор не ответил за %s", healthLoadTimeout)
		if firstText(res) != "" {
			errText = "нет в ответе list_servers"
		}
		out.Servers[i].Error = errText
	}
	return out
}

func healthError(out setHealth, err error) setHealth {
	for i := range out.Servers {
		out.Servers[i].Error = truncate(err.Error(), 500)
	}
	return out
}

// noteHealth пишет в stdout (его забирает systemd) только смену состояния:
// сервер снова здоров — молчим, стал нездоровым — одна строка. Иначе журнал
// забивается одной и той же строкой каждый цикл опроса.
func (m *Manager) noteHealth(id string, view setHealth) {
	c := m.health
	c.mu.Lock()
	prev := c.last[id]
	if prev == nil {
		prev = map[string]bool{}
		c.last[id] = prev
	}
	type bad struct{ name, reason string }
	var became []bad
	for _, s := range view.Servers {
		if s.Healthy == nil {
			continue
		}
		if *s.Healthy {
			prev[s.Name] = true
			continue
		}
		if was, known := prev[s.Name]; !known || was {
			became = append(became, bad{s.Name, s.Error})
		}
		prev[s.Name] = false
	}
	c.mu.Unlock()
	for _, b := range became {
		m.log.Warn("набор "+id+": сервер "+b.name+" unhealthy", "reason", b.reason)
	}
}

func firstText(res *mcp.CallToolResult) string {
	if res == nil {
		return ""
	}
	for _, c := range res.Content {
		if text, ok := c.(*mcp.TextContent); ok {
			return text.Text
		}
	}
	return ""
}
