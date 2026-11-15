package registry

import (
	"encoding/json"
	"strings"
	"testing"
)

// rateLimit — поле набора: битое значение отвергается с именем поля, годное
// сохраняется, null снимает блок.
func TestRateLimitPatchSemantics(t *testing.T) {
	store := openStore(t, t.TempDir())
	addServer(t, store, `{"name":"fs","command":["npx","x"]}`)

	bad := map[string]string{
		`{"name":"a","servers":["fs"],"rateLimit":{"perMinute":-1}}`:                      "rateLimit.perMinute",
		`{"name":"b","servers":["fs"],"rateLimit":{"burst":-2}}`:                          "rateLimit.burst",
		`{"name":"c","servers":["fs"],"rateLimit":{"perMinute":10,"perTool":{"":{}}}}`:    "rateLimit.perTool",
		`{"name":"d","servers":["fs"],"rateLimit":{"perTool":{"fs/x":{"perMinute":-3}}}}`: "rateLimit.perTool.fs/x.perMinute",
		`{"name":"e","servers":["fs"],"rateLimit":"120"}`:                                 "rateLimit",
		`{"name":"f","servers":["fs"],"rateLimit":120}`:                                   "rateLimit",
		// Опечатка в имени поля не должна молча снять лимит.
		`{"name":"g","servers":["fs"],"rateLimit":{"perMunute":10}}`: "rateLimit",
		// Ключ perTool обязан быть парой server/tool.
		`{"name":"h","servers":["fs"],"rateLimit":{"perTool":{"fsx":{"perMinute":5}}}}`:          "rateLimit.perTool.fsx",
		`{"name":"i","servers":["fs"],"rateLimit":{"perTool":{"fs/x":{"perTool":{"fs/y":{}}}}}}`: "rateLimit.perTool.fs/x.perTool",
	}
	for body, field := range bad {
		_, err := store.AddSet(json.RawMessage(body))
		if err == nil {
			t.Errorf("ожидалась ошибка, принят %s", body)
			continue
		}
		if !strings.Contains(err.Error(), field) {
			t.Errorf("%s: ошибка не называет %q: %v", body, field, err)
		}
	}

	set, err := store.AddSet(json.RawMessage(`{"name":"ok","servers":["fs"],"rateLimit":null}`))
	if err != nil {
		t.Fatalf("AddSet с null: %v", err)
	}
	if set.RateLimit != nil {
		t.Fatalf("null дал блок: %+v", set.RateLimit)
	}
	// Объект сохраняется как есть, включая perTool.
	if set, err = store.UpdateSet("ok", json.RawMessage(`{"rateLimit":{"perMinute":120,"burst":20,"perTool":{"fs/x":{"perMinute":10,"burst":2}}}}`)); err != nil {
		t.Fatalf("UpdateSet: %v", err)
	}
	if set.RateLimit == nil || set.RateLimit.PerMinute != 120 || set.RateLimit.Burst != 20 {
		t.Fatalf("лимит не сохранён: %+v", set.RateLimit)
	}
	if got := set.RateLimit.PerTool["fs/x"]; got.PerMinute != 10 || got.Burst != 2 {
		t.Fatalf("perTool не сохранён: %+v", set.RateLimit.PerTool)
	}
	// Поля нет — блок остаётся: правка не обязана повторять rateLimit.
	if set, err = store.UpdateSet("ok", json.RawMessage(`{"description":"правка"}`)); err != nil {
		t.Fatalf("UpdateSet без rateLimit: %v", err)
	}
	if set.RateLimit == nil || set.RateLimit.PerMinute != 120 {
		t.Fatalf("пропуск поля снял rateLimit: %+v", set.RateLimit)
	}
	// Новый объект заменяет блок целиком, а не дополняет его.
	if set, err = store.UpdateSet("ok", json.RawMessage(`{"rateLimit":{"perMinute":30}}`)); err != nil {
		t.Fatalf("UpdateSet замена: %v", err)
	}
	if set.RateLimit == nil || set.RateLimit.PerMinute != 30 || set.RateLimit.Burst != 0 || set.RateLimit.PerTool != nil {
		t.Fatalf("блок не заменён: %+v", set.RateLimit)
	}
	// null снимает блок.
	if set, err = store.UpdateSet("ok", json.RawMessage(`{"rateLimit":null}`)); err != nil {
		t.Fatalf("UpdateSet null: %v", err)
	}
	if set.RateLimit != nil {
		t.Fatalf("null не снял блок: %+v", set.RateLimit)
	}
	// Нулевой блок — «без лимита», хранится как отсутствие поля.
	if set, err = store.UpdateSet("ok", json.RawMessage(`{"rateLimit":{"perMinute":0,"burst":0}}`)); err != nil {
		t.Fatalf("UpdateSet нули: %v", err)
	}
	if set.RateLimit != nil {
		t.Fatalf("нулевой блок сохранён: %+v", set.RateLimit)
	}
}

// A zero perTool entry with a set-wide limit is a limit for other tools only:
// the block is kept.
func TestRateLimitPerToolOnly(t *testing.T) {
	store := openStore(t, t.TempDir())
	addServer(t, store, `{"name":"fs","command":["npx","x"]}`)
	set, err := store.AddSet(json.RawMessage(`{"name":"tool","servers":["fs"],"rateLimit":{"perTool":{"fs/x":{"perMinute":5}}}}`))
	if err != nil {
		t.Fatalf("AddSet: %v", err)
	}
	if set.RateLimit == nil || set.RateLimit.PerTool["fs/x"].PerMinute != 5 {
		t.Fatalf("perTool без общего лимита не сохранён: %+v", set.RateLimit)
	}
}
