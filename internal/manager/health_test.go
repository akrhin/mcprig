package manager

import (
	"context"
	"testing"
)

// Отмена запроса лидера не должна оседать в кэше как «context canceled»:
// иначе 15 секунд панель показывает ложную ошибку.
func TestHealthCacheLeaderCancelNotCached(t *testing.T) {
	c := newHealthCache()
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
