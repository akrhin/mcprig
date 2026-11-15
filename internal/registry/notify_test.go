package registry

import (
	"encoding/json"
	"testing"
)

// API отвечает 400 на мусор в notify: адрес проверяется до записи в реестр.
func TestNotifyBadWebhookRejected(t *testing.T) {
	store := openStore(t, t.TempDir())
	addServer(t, store, `{"name":"fs","command":["npx","x"]}`)

	bad := []string{
		`{"name":"a","servers":["fs"],"notify":{"webhook":"ftp://example.com/hook"}}`,
		`{"name":"b","servers":["fs"],"notify":{"webhook":"example.com/hook"}}`,
		`{"name":"c","servers":["fs"],"notify":{"webhook":"https://"}}`,
		`{"name":"d","servers":["fs"],"notify":"https://example.com/hook"}`,
		`{"name":"e","servers":["fs"],"notify":{"webhook":42}}`,
	}
	for _, body := range bad {
		if _, err := store.AddSet(json.RawMessage(body)); err == nil {
			t.Errorf("ожидалась ошибка, принят %s", body)
		}
	}

	// null — уведомлений нет; годный адрес сохраняется как есть.
	set, err := store.AddSet(json.RawMessage(`{"name":"ok","servers":["fs"],"notify":null}`))
	if err != nil {
		t.Fatalf("AddSet с null: %v", err)
	}
	if set.Notify != nil {
		t.Fatalf("null дал блок: %+v", set.Notify)
	}
	set, err = store.UpdateSet("ok", json.RawMessage(`{"notify":{"webhook":"https://example.com/hook"}}`))
	if err != nil {
		t.Fatalf("UpdateSet: %v", err)
	}
	if set.Notify == nil || set.Notify.Webhook != "https://example.com/hook" {
		t.Fatalf("notify не сохранён: %+v", set.Notify)
	}
	// Поля нет — блок остаётся: правка не обязана повторять notify.
	if set, err = store.UpdateSet("ok", json.RawMessage(`{"description":"правка"}`)); err != nil {
		t.Fatalf("UpdateSet без notify: %v", err)
	}
	if set.Notify == nil || set.Notify.Webhook != "https://example.com/hook" {
		t.Fatalf("пропуск поля снял notify: %+v", set.Notify)
	}
	// Объект заменяет адрес целиком.
	if set, err = store.UpdateSet("ok", json.RawMessage(`{"notify":{"webhook":"https://other.example/h"}}`)); err != nil {
		t.Fatalf("UpdateSet замена: %v", err)
	}
	if set.Notify == nil || set.Notify.Webhook != "https://other.example/h" {
		t.Fatalf("адрес не заменён: %+v", set.Notify)
	}
	// null снимает блок, даже когда он был.
	if set, err = store.UpdateSet("ok", json.RawMessage(`{"notify":null}`)); err != nil {
		t.Fatalf("UpdateSet null: %v", err)
	}
	if set.Notify != nil {
		t.Fatalf("null не снял блок: %+v", set.Notify)
	}
	// Пустой адрес снимает блок: правка не должна требовать null.
	if _, err = store.UpdateSet("ok", json.RawMessage(`{"notify":{"webhook":"https://example.com/hook"}}`)); err != nil {
		t.Fatalf("UpdateSet: %v", err)
	}
	if set, err = store.UpdateSet("ok", json.RawMessage(`{"notify":{"webhook":""}}`)); err != nil {
		t.Fatalf("UpdateSet пустой: %v", err)
	}
	if set.Notify != nil {
		t.Fatalf("пустой webhook не снял блок: %+v", set.Notify)
	}
	if _, err = store.UpdateSet("ok", json.RawMessage(`{"notify":"https://example.com/hook"}`)); err == nil {
		t.Fatal("UpdateSet принял notify строкой")
	}
}
