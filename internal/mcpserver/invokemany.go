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
	Calls []json.RawMessage `json:"calls"`
	// shortcut: суммарный размер ответа не ограничен, потолок на каждый вызов; поднять при жалобе на 8×maxBytes
	MaxBytes int `json:"maxBytes,omitempty"`
}

type manyCall struct {
	Server      string          `json:"server"`
	CommandName string          `json:"command_name"`
	Command     string          `json:"command"` // синоним command_name: list_commands называет это поле command
	Parameters  json.RawMessage `json:"parameters,omitempty"`
	// TimeoutMs — предел этого вызова; 0 — requestTimeout набора.
	TimeoutMs int `json:"timeoutMs,omitempty"`
}

// manyAnswer is one entry of the fan-out answer. Result carries what
// invoke_command would return — text for a plain answer, the whole result
// object otherwise — capped at maxBytes. A failed call keeps its slot with
// ok:false and error, so the order always matches calls; a policy refusal
// carries in result the same shape invoke_command returns.
type manyAnswer struct {
	OK          bool   `json:"ok"`
	Server      string `json:"server"`
	CommandName string `json:"command_name"`
	Result      any    `json:"result,omitempty"`
	Error       string `json:"error,omitempty"`
	MS          int64  `json:"ms"`
	// TimeoutMS — предел, под которым шёл этот вызов: свой timeoutMs или
	// requestTimeout набора. 0 — предела не было.
	TimeoutMS int `json:"timeout_ms,omitempty"`
}

func (m *Manager) invokeMany(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	var in invokeManyInput
	if err := strictArgs(req, &in, "каждый вызов: server, command_name (синоним — command), parameters, timeoutMs", "calls", "maxBytes"); err != nil {
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
	// ответ на восемь requestTimeout подряд. Свой timeoutMs вызова бюджет
	// поднимает — иначе вызов, попросивший больше, срезался бы раньше срока;
	// предел самого вызова ставится ниже, после получения слота.
	// requestTimeout 0 — предела нет: дедлайн соседа не должен срезать вызов,
	// который своего timeoutMs не просил.
	budget := m.config().RequestTimeout
	if budget > 0 {
		for _, c := range calls {
			if c.TimeoutMs > budget {
				budget = c.TimeoutMs
			}
		}
		callCtx, cancel := m.callCtxFor(ctx, budget)
		defer cancel()
		ctx = callCtx
	}
	label := labelOf(ctx, req)

	out := make([]manyAnswer, len(calls))
	slots := m.slots
	var wg sync.WaitGroup
	for i, c := range calls {
		wg.Add(1)
		go func() {
			defer wg.Done()
			start := time.Now()
			limit := m.callTimeoutMs(c.TimeoutMs)
			ans := manyAnswer{Server: c.Server, CommandName: c.CommandName, TimeoutMS: limit}
			e := m.find(c.Server)
			if e == nil {
				ans.Error = fmt.Sprintf("неизвестный сервер %q", c.Server)
				ans.MS = time.Since(start).Milliseconds()
				out[i] = ans
				return
			}
			// Лимит считается на каждый внутренний вызов, как у invoke_command.
			if err := m.throttle(ctx, e.server().Name, c.CommandName); err != nil {
				ans.Error = err.Error()
				ans.MS = time.Since(start).Milliseconds()
				out[i] = ans
				return
			}
			// Слот ключуется именем: запись перезагрузка подменяет, имя — нет.
			slot := slots.get(e.server().Name)
			wait := time.Now()
			select {
			case slot <- struct{}{}:
				defer func() { <-slot }()
				// Свой предел — только после слота: очередь не должна
				// съедать таймаут самого вызова.
				callCtx, stop := m.callCtxFor(ctx, limit)
				if err := m.runManyCall(callCtx, label, e, c, limit, maxBytes, &ans); err != nil {
					ans.Error = err.Error()
				}
				stop()
			case <-ctx.Done():
				// Слота не дождались, вызов не уходил: это очередь, а не
				// молчание апстрима, и отказ обязан это сказать.
				ans.Error = fmt.Sprintf("нет свободного слота у сервера %s (лимит %d): ждали %s, вызов не отправлен: %v",
					e.server().Name, slots.limit, time.Since(wait).Round(time.Millisecond), ctx.Err())
			}
			ans.MS = time.Since(start).Milliseconds()
			out[i] = ans
		}()
	}
	wg.Wait()
	res, err := jsonResult(out)
	if err != nil {
		return res, err
	}
	// Как у одиночного вызова: упало всё — ответ ошибка, а не успех с
	// ошибками внутри. Частичный отказ остаётся данными.
	failed := 0
	for _, ans := range out {
		if !ans.OK {
			failed++
		}
	}
	if len(out) > 0 && failed == len(out) {
		res.IsError = true
	}
	// Бюджет веера — фактический предел слота и очереди: с ним агент
	// сравнивает всю пачку, у элементов свой timeout_ms.
	return withTimeoutMeta(res, budget), nil
}

// parseManyCalls validates every call before anything runs: a malformed entry
// fails the request rather than half of it.
func parseManyCalls(raw []json.RawMessage) ([]manyCall, error) {
	calls := make([]manyCall, len(raw))
	for i, item := range raw {
		extra, err := extraKeys(item, "server", "command_name", "command", "parameters", "timeoutMs")
		if err != nil {
			return nil, fmt.Errorf("calls[%d]: нужен объект вызова", i)
		}
		if len(extra) > 0 {
			return nil, fmt.Errorf("calls[%d]: лишние ключи: %s. Допустимые: server, command_name, command, parameters, timeoutMs. Аргументы кладутся в parameters", i, strings.Join(extra, ", "))
		}
		if err := json.Unmarshal(item, &calls[i]); err != nil {
			if text, ok := typeErrorText(err, fmt.Sprintf("calls[%d]: ", i)); ok {
				return nil, errors.New(text)
			}
			return nil, fmt.Errorf("calls[%d]: %w", i, err)
		}
		if calls[i].Server == "" {
			return nil, fmt.Errorf("calls[%d]: укажите server", i)
		}
		if calls[i].CommandName == "" {
			calls[i].CommandName = calls[i].Command
		} else if calls[i].Command != "" && calls[i].Command != calls[i].CommandName {
			return nil, fmt.Errorf("calls[%d]: укажите command_name (или command)", i)
		}
		if calls[i].CommandName == "" {
			return nil, fmt.Errorf("calls[%d]: укажите command_name (или command)", i)
		}
		if _, err := parametersOf(calls[i].Parameters); err != nil {
			return nil, fmt.Errorf("calls[%d]: %w", i, err)
		}
		if err := checkTimeoutMs(calls[i].TimeoutMs); err != nil {
			return nil, fmt.Errorf("calls[%d]: %w", i, err)
		}
	}
	return calls, nil
}

// runManyCall executes one call and fills ans. A downstream isError counts as a
// failed call, as it does in the counter. limit — предел, под которым вызов
// шёл: свой timeoutMs или requestTimeout набора, 0 — предела не было.
func (m *Manager) runManyCall(ctx context.Context, label string, e *entry, c manyCall, limit, maxBytes int, ans *manyAnswer) error {
	// Снимок на весь вызов: перезагрузка может подменить конфиг посередине,
	// а вызов должен проверяться по одному правилу, не по двум.
	srv := e.server()
	if !usable(srv) {
		return serverProblem(srv)
	}
	if message, denied := m.gate(ctx, srv.Name, c.CommandName, srv.Permissions); denied {
		// Тот же shape, что у invoke_command: маркер в content и Meta с
		// lazyMcpPolicy — хук-клиент ищет их там, а не в строке error.
		blocked := blockedResult(srv.Name, c.CommandName, message)
		ans.Result = blocked
		ans.Error = resultMessage(blocked)
		return nil
	}
	// Та же сверка, что у invoke_command: неизвестное имя не идёт в апстрим,
	// иначе отказ приходит тремя разными текстами. Пустой кэш каталог не
	// отменяет — см. toolForCall.
	tool, err := m.toolForCall(ctx, e, srv, c.CommandName)
	if err != nil {
		return err
	}
	if tool != nil {
		// Один изменившийся инструмент не тянет за собой соседние: отбит
		// только он, остальные вызовы пачки идут как обычно.
		if message, changed := m.quarantine(srv.Name, tool); changed {
			ans.Result = quarantineResult(message)
			ans.Error = message
			return nil
		}
	}
	args, err := parametersOf(c.Parameters)
	if err != nil {
		return err
	}

	res, err := m.callWithRetry(ctx, label, e, c.CommandName, args)
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return m.callTimeout(ctx, limit, e.server().Name, c.CommandName)
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
	value, err := m.truncateValue(ctx, res, maxBytes)
	if err != nil {
		return err
	}
	ans.OK = true
	ans.Result = value
	return nil
}

// serverSlots hands out one bounded channel per server, created on first use.
// Keyed by the server name: a reload swaps the entry but not the name, so the
// limit stays shared instead of briefly doubling, and the map stays as small as
// the set — only a name that resolved to a server reaches here.
type serverSlots struct {
	mu    sync.Mutex
	limit int
	byKey map[string]chan struct{}
}

func newServerSlots(limit int) *serverSlots {
	return &serverSlots{limit: limit, byKey: map[string]chan struct{}{}}
}

func (s *serverSlots) get(name string) chan struct{} {
	s.mu.Lock()
	defer s.mu.Unlock()
	ch := s.byKey[name]
	if ch == nil {
		ch = make(chan struct{}, s.limit)
		s.byKey[name] = ch
	}
	return ch
}
