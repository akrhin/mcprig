package registry

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// Параллельные обработчики бьют в магазин одновременно: ни гонок (их ловит
// -race), ни потерянных записей — реестр после прогона перечитывается с диска
// целиком.
func TestStoreConcurrentWriters(t *testing.T) {
	dir := t.TempDir()
	store := openStore(t, dir)
	addServer(t, store, `{"name":"fs","command":["x"]}`)
	addSet(t, store, `{"name":"demo","servers":["fs"],"port":`+strconv.Itoa(freePort(t))+`}`)

	const writers, rounds = 4, 5
	var wg sync.WaitGroup
	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for r := 0; r < rounds; r++ {
				name := fmt.Sprintf("srv-%d-%d", w, r)
				if _, err := store.AddServer(json.RawMessage(`{"name":"` + name + `","command":["x"]}`)); err != nil {
					t.Errorf("AddServer(%s): %v", name, err)
				}
				if _, err := store.UpdateSet("demo", json.RawMessage(`{"description":"`+name+`"}`)); err != nil {
					t.Errorf("UpdateSet(%s): %v", name, err)
				}
				// Лимит действующих ключей — не сбой: лишние выдачи отвергаются.
				if _, err := store.IssueKey("demo", "", name); err != nil && !strings.Contains(err.Error(), "действующих ключей") {
					t.Errorf("IssueKey(%s): %v", name, err)
				}
				_ = store.Sets()
				_ = store.Set("demo")
			}
		}(w)
	}
	wg.Wait()

	// Все писатели дошли до диска: ни одна правка не потерялась.
	reopened := openStore(t, dir)
	if got, want := len(reopened.Servers()), 1+writers*rounds; got != want {
		t.Fatalf("серверов %d, ожидалось %d", got, want)
	}
	set := reopened.Set("demo")
	if set == nil {
		t.Fatal("набор demo исчез")
	}
	if got := activeCount(set.Keys); got != MaxActiveKeys {
		t.Fatalf("действующих ключей %d, ожидалось %d", got, MaxActiveKeys)
	}
}
