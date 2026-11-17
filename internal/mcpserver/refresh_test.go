package mcpserver

import (
	"context"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/akrhin/mcprig/internal/config"
)

// refresh:true сбрасывает кэш не чаще окна на пару (клиент, сервер): второй
// запрос подряд получает кэш и признак отсрочки, а не пустой каталог.
func TestRefreshCatalogsThrottled(t *testing.T) {
	var dials int32
	m := testManager(&config.Config{Servers: []*config.Server{stdio("alpha")}}, &dials, 0)
	t.Cleanup(func() { _ = m.Close() })
	e := m.find("alpha")
	ctx := context.Background()

	m.storeTools(e, []*mcp.Tool{{Name: "echo"}})
	if deferred := m.refreshCatalogs(ctx, true, []string{"alpha"}); len(deferred) != 0 {
		t.Fatalf("первый refresh отложен: %v", deferred)
	}
	if e.cachedTools() != nil {
		t.Fatal("refresh не сбросил кэш")
	}

	m.storeTools(e, []*mcp.Tool{{Name: "echo"}})
	if deferred := m.refreshCatalogs(ctx, true, []string{"alpha"}); len(deferred) != 1 || deferred[0] != "alpha" {
		t.Fatalf("второй refresh не отложен: %v", deferred)
	}
	if e.cachedTools() == nil {
		t.Fatal("отложенный refresh всё-таки сбросил кэш")
	}

	// Другой ключ своё окно не занимал.
	other := keyCtx()
	if deferred := m.refreshCatalogs(other, true, []string{"alpha"}); len(deferred) != 0 {
		t.Fatalf("соседний ключ получил отсрочку: %v", deferred)
	}
}

// Отложенный refresh — не ошибка: ответ остаётся каталогом, а отсрочка видна
// полем refreshDeferred.
func TestRefreshDeferredIsReported(t *testing.T) {
	var dials int32
	m := testManager(&config.Config{Servers: []*config.Server{stdio("alpha")}}, &dials, 0)
	t.Cleanup(func() { _ = m.Close() })
	ctx := context.Background()

	res, err := m.listCommands(ctx, toolReq("list_commands", `{"server":"alpha","refresh":true}`))
	if err != nil || res == nil {
		t.Fatalf("listCommands: %v", err)
	}
	if res.Meta != nil {
		t.Fatalf("первый refresh помечен отсроченным: %v", res.Meta)
	}

	res, err = m.listCommands(ctx, toolReq("list_commands", `{"server":"alpha","refresh":true}`))
	if err != nil {
		t.Fatalf("listCommands: %v", err)
	}
	if res.IsError {
		t.Fatalf("отложенный refresh выдан за ошибку: %s", resultText(t, res))
	}
	if res.Meta["refreshDeferred"] == nil {
		t.Fatalf("нет признака отсрочки: %v", res.Meta)
	}
	if names := listNames(t, res); !hasName(names, "echo") {
		t.Fatalf("отсрочка спрятала каталог: %v", names)
	}
}
