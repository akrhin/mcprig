package manager

import (
	"context"
	"testing"
	"time"
)

// Отмена запроса лидера не должна оседать в кэше как «context canceled»:
// иначе 15 секунд панель показывает ложную ошибку.
func TestHealthCacheLeaderCancelNotCached(t *testing.T) {
	c := newHealthCache(nil)
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // лидер уже отменён до загрузки

	load := func(ctx context.Context) setHealth {
		if err := ctx.Err(); err != nil {
			return setHealth{Servers: []serverHealth{{Name: "s", Error: err.Error()}}}
		}
		return setHealth{Servers: []serverHealth{{Name: "s"}}}
	}
	if got := c.get(ctx, "s", load); got.Servers[0].Error != "" {
		t.Fatalf("отмена лидера попала в ответ: %+v", got)
	}

	got := c.get(context.Background(), "s", func(context.Context) setHealth {
		t.Fatal("загрузка повторилась: отмена закэширована")
		return setHealth{}
	})
	if got.Servers[0].Error != "" {
		t.Fatalf("в кэше отмена: %+v", got)
	}
}

// Паника пробы не оставляет wait открытым: первый запрос получает пустой вид, а
// следующий не висит и повторяет пробу.
func TestHealthCachePanicDoesNotHang(t *testing.T) {
	c := newHealthCache(nil)
	calls := 0
	load := func(context.Context) setHealth {
		calls++
		if calls == 1 {
			panic("проба упала")
		}
		return setHealth{Servers: []serverHealth{{Name: "s"}}}
	}
	if got := c.get(context.Background(), "s", load); got.Servers != nil {
		t.Fatalf("паника отдала вид: %+v", got)
	}

	done := make(chan setHealth, 1)
	go func() { done <- c.get(context.Background(), "s", load) }()
	select {
	case got := <-done:
		if len(got.Servers) != 1 || got.Servers[0].Name != "s" {
			t.Fatalf("повторный вызов: %+v", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("второй get повис после паники пробы")
	}
	if calls != 2 {
		t.Fatalf("проб = %d, ожидалось 2", calls)
	}
}
