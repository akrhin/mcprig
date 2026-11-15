package registry

import (
	"encoding/json"
	"strconv"
	"testing"
)

// Профиль живёт в записи набора по трём состояниям, как notify: правка без
// поля его сохраняет, null снимает, объект заменяет.
func TestProfilesSurviveSetUpdate(t *testing.T) {
	store := openStore(t, t.TempDir())
	addServer(t, store, `{"name":"fs","command":["/bin/true"]}`)
	set := addSet(t, store, `{"name":"demo","servers":["fs"],"port":`+strconv.Itoa(freePort(t))+
		`,"profiles":{"reader":{"deny":["*/*_write"],"allow":["*"]}}}`)
	if p := set.Profiles["reader"]; p == nil || len(p.Deny) != 1 || p.Deny[0] != "*/*_write" {
		t.Fatalf("профиль не сохранён: %+v", set.Profiles)
	}

	// Правка набора без поля профили не трогает.
	set, err := store.UpdateSet("demo", json.RawMessage(`{"description":"d"}`))
	if err != nil {
		t.Fatalf("UpdateSet: %v", err)
	}
	if set.Profiles["reader"] == nil {
		t.Fatal("правка без поля снесла профили")
	}

	// Ключ с профилем переживает правку набора: профиль хранится на ключе.
	if _, err := store.IssueKey("demo", "reader", ""); err != nil {
		t.Fatalf("IssueKey: %v", err)
	}
	set, err = store.UpdateSet("demo", json.RawMessage(`{"description":"e"}`))
	if err != nil {
		t.Fatalf("UpdateSet: %v", err)
	}
	var found bool
	for _, key := range set.Keys {
		found = found || (key.RevokedAt == "" && key.Profile == "reader")
	}
	if !found {
		t.Fatalf("активного ключа с профилем нет: %+v", set.Keys)
	}

	// Публичный вид: имена профилей и профиль ключа — для панели.
	pub := PublicSet(set)
	if len(pub.Profiles) != 1 || pub.Profiles[0] != "reader" {
		t.Fatalf("имена профилей: %v", pub.Profiles)
	}
	if last := pub.Keys[len(pub.Keys)-1]; last.Profile != "reader" {
		t.Fatalf("профиль ключа в публичном виде: %+v", last)
	}

	// Мусор отвергается, null снимает.
	for _, bad := range []string{
		`{"profiles":{"reader":{"denny":["*"]}}}`,
		`{"profiles":{"reader":{"deny":[""]}}}`,
		`{"profiles":"reader"}`,
	} {
		if _, err := store.UpdateSet("demo", json.RawMessage(bad)); err == nil {
			t.Errorf("принят мусор: %s", bad)
		}
	}
	set, err = store.UpdateSet("demo", json.RawMessage(`{"profiles":null}`))
	if err != nil {
		t.Fatalf("снятие профилей: %v", err)
	}
	if len(set.Profiles) != 0 {
		t.Fatalf("null не снял профили: %+v", set.Profiles)
	}
}

// Выдача ключа с профилем пишет имя в запись ключа и не проверяет его:
// проверка имён — дело панели, запись хранит то, что дали.
func TestIssueKeyStoresProfile(t *testing.T) {
	store := openStore(t, t.TempDir())
	addServer(t, store, `{"name":"fs","command":["/bin/true"]}`)
	addSet(t, store, `{"name":"demo","servers":["fs"],"port":`+strconv.Itoa(freePort(t))+
		`,"profiles":{"reader":{"deny":["*"]}}}`)

	if _, err := store.IssueKey("demo", " reader ", ""); err != nil {
		t.Fatalf("IssueKey: %v", err)
	}
	set := store.Set("demo")
	if active := set.Keys[len(set.Keys)-1]; active.Profile != "reader" {
		t.Fatalf("профиль ключа = %q", active.Profile)
	}
}
