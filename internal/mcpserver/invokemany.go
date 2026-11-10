package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	// maxFanOut caps one invoke_many request: a larger batch belongs in several
	// requests, where the caller can react to failures halfway.
	maxFanOut = 8
	// maxPerServer bounds simultaneous calls to one downstream inside a fan-out:
	// eight at once made a single downstream drop the whole batch.
	maxPerServer = 4
)

type invokeManyInput struct {
	Calls    []json.RawMessage `json:"calls"`
	MaxBytes int               `json:"maxBytes,omitempty"`
}

type manyCall struct {
	Server      string          `json:"server"`
	CommandName string          `json:"command_name"`
	Command     string          `json:"command"` // синоним command_name: list_commands называет это поле command
	Parameters  json.RawMessage `json:"parameters,omitempty"`
}

// manyAnswer is one entry of the fan-out answer. Result carries what
// invoke_command would return — text for a plain answer, the whole result
// object otherwise — capped at maxBytes. A failed call keeps its slot with
// ok:false and error, so the order always matches calls.
type manyAnswer struct {
	OK          bool   `json:"ok"`
	Server      string `json:"server"`
	CommandName string `json:"command_name"`
	Result      any    `json:"result,omitempty"`
	Error       string `json:"error,omitempty"`
	MS          int64  `json:"ms"`
}

func (m *Manager) invokeMany(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	var in invokeManyInput
	if err := strictArgs(req, &in, "каждый вызов: server, command_name (синоним — command), parameters", "calls", "maxBytes"); err != nil {
		return errResult(err)
	}
	if len(in.Calls) == 0 {
		return errResult(errors.New("calls: нужен хотя бы один вызов"))
	}
	if len(in.Calls) > maxFanOut {
		return errResult(fmt.Errorf("calls: допустимо не больше %d вызовов", maxFanOut))
	}
	maxBytes, err := resolveMaxBytes(in.MaxBytes)
	if err != nil {
		return errResult(err)
	}
	calls, err := parseManyCalls(in.Calls)
	if err != nil {
		return errResult(err)
	}

	// Один бюджет на весь веер: медленный downstream не должен растягивать
	// ответ на восемь requestTimeout подряд.
	ctx, cancel := m.callCtx(ctx)
	defer cancel()
	label := labelOf(ctx, req)

	out := make([]manyAnswer, len(calls))
	slots := newServerSlots(maxPerServer)
	var wg sync.WaitGroup
	for i, c := range calls {
		wg.Add(1)
		go func() {
			defer wg.Done()
			start := time.Now()
			ans := manyAnswer{Server: c.Server, CommandName: c.CommandName}
			slot := slots.get(c.Server)
			select {
			case slot <- struct{}{}:
				defer func() { <-slot }()
				if err := m.runManyCall(ctx, label, c, maxBytes, &ans); err != nil {
					ans.Error = err.Error()
				}
			case <-ctx.Done():
				ans.Error = m.timeoutText()
			}
			ans.MS = time.Since(start).Milliseconds()
			out[i] = ans
		}()
	}
	wg.Wait()
	return jsonResult(out)
}

// parseManyCalls validates every call before anything runs: a malformed entry
// fails the request rather than half of it.
func parseManyCalls(raw []json.RawMessage) ([]manyCall, error) {
	calls := make([]manyCall, len(raw))
	for i, item := range raw {
		extra, err := extraKeys(item, "server", "command_name", "command", "parameters")
		if err != nil {
			return nil, fmt.Errorf("calls[%d]: ожидался объект", i)
		}
		if len(extra) > 0 {
			return nil, fmt.Errorf("calls[%d]: лишние ключи: %s — допустимые: server, command_name, command, parameters; аргументы кладутся в parameters", i, strings.Join(extra, ", "))
		}
		if err := json.Unmarshal(item, &calls[i]); err != nil {
			if text, ok := typeErrorText(err, fmt.Sprintf("calls[%d]: ", i)); ok {
				return nil, errors.New(text)
			}
			return nil, fmt.Errorf("calls[%d]: %w", i, err)
		}
		if calls[i].Server == "" {
			return nil, fmt.Errorf("calls[%d]: server обязателен", i)
		}
		if calls[i].CommandName == "" {
			calls[i].CommandName = calls[i].Command
		} else if calls[i].Command != "" && calls[i].Command != calls[i].CommandName {
			return nil, fmt.Errorf("calls[%d]: укажите одно: command_name или command", i)
		}
		if calls[i].CommandName == "" {
			return nil, fmt.Errorf("calls[%d]: command_name обязателен", i)
		}
		if _, err := parametersOf(calls[i].Parameters); err != nil {
			return nil, fmt.Errorf("calls[%d]: %w", i, err)
		}
	}
	return calls, nil
}

// runManyCall executes one call and fills ans. A downstream isError counts as a
// failed call, as it does in the counter.
func (m *Manager) runManyCall(ctx context.Context, label string, c manyCall, maxBytes int, ans *manyAnswer) error {
	e := m.find(c.Server)
	if e == nil {
		return fmt.Errorf("неизвестный сервер %q", c.Server)
	}
	if !usable(e.srv) {
		return serverProblem(e.srv)
	}
	if message, denied := denyReason(m.cfg.Permissions, e.srv.Permissions, e.srv.Name, c.CommandName); denied {
		if message == "" {
			message = "tool '" + e.srv.Name + ":" + c.CommandName + "' is denied"
		}
		return errors.New("🚫 Blocked by mcprig policy: " + message)
	}
	// Та же сверка, что у invoke_command: неизвестное имя не идёт в апстрим,
	// иначе отказ приходит тремя разными текстами. Пустой кэш — как раньше.
	if cached := e.cachedTools(); cached != nil && !hasTool(cached, c.CommandName) {
		return fmt.Errorf("неизвестный инструмент %q у сервера %q", c.CommandName, e.srv.Name)
	}
	args, err := parametersOf(c.Parameters)
	if err != nil {
		return err
	}

	res, err := m.callWithRetry(ctx, label, e, c.CommandName, args)
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return errors.New(m.timeoutText())
		}
		return err
	}
	if res == nil {
		ans.OK = true
		return nil
	}
	if res.IsError {
		return errors.New(resultMessage(res))
	}
	value, err := truncateValue(res, maxBytes)
	if err != nil {
		return err
	}
	ans.OK = true
	ans.Result = value
	return nil
}

func (m *Manager) timeoutText() string {
	return fmt.Sprintf("таймаут вызова %d мс", m.cfg.RequestTimeout)
}

// serverSlots hands out one bounded channel per server, created on first use.
type serverSlots struct {
	mu    sync.Mutex
	limit int
	byKey map[string]chan struct{}
}

func newServerSlots(limit int) *serverSlots {
	return &serverSlots{limit: limit, byKey: map[string]chan struct{}{}}
}

func (s *serverSlots) get(key string) chan struct{} {
	s.mu.Lock()
	defer s.mu.Unlock()
	ch := s.byKey[key]
	if ch == nil {
		ch = make(chan struct{}, s.limit)
		s.byKey[key] = ch
	}
	return ch
}
