package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"go.opentelemetry.io/otel"

	"github.com/akrhin/mcprig/internal/config"
)

// Инвентарь подчиняется тем же правилам, что и чтение: иначе список отдаёт
// адреса, закрытые правилом или профилем ключа.

func TestListResourcesGatedByDefaultDeny(t *testing.T) {
	srv := stdio("alpha")
	srv.Expose = []string{config.ExposeResources}
	cfg := &config.Config{
		Servers: []*config.Server{srv},
		Permissions: &config.Permissions{
			Default: config.PermissionDeny,
			Rules: []config.Rule{
				{Pattern: "resource:test://open/one", Action: config.PermissionAllow},
			},
		},
	}
	m := resourceManager(t, cfg)

	res, err := m.listResources(context.Background(), toolReq("list_resources", `{"server":"alpha"}`))
	if err != nil {
		t.Fatalf("listResources: %v", err)
	}
	var listed []resourceInfo
	if err := json.Unmarshal([]byte(resultText(t, res)), &listed); err != nil {
		t.Fatalf("list_resources: %v (%s)", err, resultText(t, res))
	}
	if len(listed) != 1 || listed[0].URI != "test://open/one" {
		t.Fatalf("default:deny пустил в список лишнее: %+v", listed)
	}
}

func TestListPromptsFilteredByProfile(t *testing.T) {
	srv := stdio("alpha")
	srv.Expose = []string{config.ExposePrompts}
	m := New(&config.Config{
		Servers:   []*config.Server{srv},
		Profiles:  map[string]*config.Profile{"reader": {Deny: []string{"prompt:greet"}}},
		Transport: &config.HTTPTransport{KeyProfiles: map[string]string{readerFP: "reader"}},
	}, "test", nil)
	m.dial = func(ctx context.Context, _ *config.Server) (*mcp.ClientSession, error) {
		return resourceUpstream(ctx)
	}
	t.Cleanup(func() { _ = m.Close() })

	res, err := m.listPrompts(keyCtx(), toolReq("list_prompts", `{"server":"alpha"}`))
	if err != nil {
		t.Fatalf("listPrompts: %v", err)
	}
	var listed []promptInfo
	if err := json.Unmarshal([]byte(resultText(t, res)), &listed); err != nil {
		t.Fatalf("list_prompts: %v (%s)", err, resultText(t, res))
	}
	if len(listed) != 0 {
		t.Fatalf("профиль отдал запрещённый промпт: %+v", listed)
	}
}

func TestListResourcesRejectsNegativeMaxBytes(t *testing.T) {
	srv := stdio("alpha")
	srv.Expose = []string{config.ExposeResources}
	m := resourceManager(t, &config.Config{Servers: []*config.Server{srv}})

	res, err := m.listResources(context.Background(), toolReq("list_resources", `{"server":"alpha","maxBytes":-1}`))
	if err == nil && !res.IsError {
		t.Fatalf("отрицательный maxBytes принят: %s", resultText(t, res))
	}
	text := ""
	if err != nil {
		text = err.Error()
	} else {
		text = resultText(t, res)
	}
	if !strings.Contains(text, "maxBytes: не может быть отрицательным") {
		t.Fatalf("другой текст отказа: %q", text)
	}
}

func TestListCallsAreRateLimited(t *testing.T) {
	srv := stdio("alpha")
	srv.Expose = []string{config.ExposeResources}
	m := resourceManager(t, &config.Config{
		Servers:   []*config.Server{srv},
		RateLimit: &config.RateLimit{PerMinute: 1, Burst: 1},
	})

	if res, err := m.listResources(context.Background(), toolReq("list_resources", `{"server":"alpha"}`)); err != nil || res.IsError {
		t.Fatalf("первый вызов: %v %s", err, resultText(t, res))
	}
	res, err := m.listResources(context.Background(), toolReq("list_resources", `{"server":"alpha"}`))
	if err == nil && !res.IsError {
		t.Fatalf("второй вызов не отбит лимитом: %s", resultText(t, res))
	}
	text := ""
	if err != nil {
		text = err.Error()
	} else {
		text = resultText(t, res)
	}
	if !strings.Contains(text, "лимит") {
		t.Fatalf("другой текст отказа: %q", text)
	}
}

// Сервер по имени error не должен терять свой отказ: сводка всех отказов
// лежит отдельным ключом.
func TestCollectKeepsErrorKeyForServerNamedError(t *testing.T) {
	failing := stdio("error")
	failing.Expose = []string{config.ExposeResources}
	other := stdio("beta")
	other.Expose = []string{config.ExposeResources}
	m := New(&config.Config{Servers: []*config.Server{failing, other}}, "test", nil)
	m.dial = func(ctx context.Context, srv *config.Server) (*mcp.ClientSession, error) {
		return nil, errors.New("канал недоступен")
	}
	t.Cleanup(func() { _ = m.Close() })

	res, err := m.listResources(context.Background(), toolReq("list_resources", `{}`))
	if err != nil || res.IsError {
		t.Fatalf("list_resources: %v %s", err, resultText(t, res))
	}
	var out map[string]json.RawMessage
	if err := json.Unmarshal([]byte(resultText(t, res)), &out); err != nil {
		t.Fatalf("list_resources: %v (%s)", err, resultText(t, res))
	}
	var summary string
	if err := json.Unmarshal(out["_errors"], &summary); err != nil || summary == "" {
		t.Fatalf("сводка отказов не под _errors: %s", resultText(t, res))
	}
	var perServer map[string]string
	if err := json.Unmarshal(out["error"], &perServer); err != nil || perServer["error"] == "" {
		t.Fatalf("отказ сервера error перетёрт: %s", resultText(t, res))
	}
}

// Текст ошибки апстрима повторяет ключи; в спан уходит только тип ошибки.
func TestSpanErrorTextIsScrubbed(t *testing.T) {
	rec := recorderProvider(t)
	const secret = "sk-live-0123456789"

	_, span := otel.Tracer(tracerName).Start(context.Background(), toolCallSpan)
	recordOutcome(span, nil, fmt.Errorf("апстрим отверг ключ %s", secret))
	span.End()

	spans := spansNamed(rec, toolCallSpan)
	if len(spans) != 1 {
		t.Fatalf("спанов %s: %d, ждали 1", toolCallSpan, len(spans))
	}
	if desc := spans[0].Status().Description; strings.Contains(desc, secret) {
		t.Fatalf("секрет в статусе спана: %q", desc)
	}
	for _, ev := range spans[0].Events() {
		for _, kv := range ev.Attributes {
			if text := kv.Value.AsString(); strings.Contains(text, secret) {
				t.Fatalf("секрет в событии спана %s: %q", kv.Key, text)
			}
		}
	}
}
