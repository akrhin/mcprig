package registry

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func openStore(t *testing.T, dir string) *Store {
	t.Helper()
	store, err := Open(dir)
	if err != nil {
		t.Fatalf("Open(%s): %v", dir, err)
	}
	return store
}

func addServer(t *testing.T, store *Store, body string) Server {
	t.Helper()
	srv, err := store.AddServer(json.RawMessage(body))
	if err != nil {
		t.Fatalf("AddServer(%s): %v", body, err)
	}
	return srv
}

func addSet(t *testing.T, store *Store, body string) *Set {
	t.Helper()
	set, err := store.AddSet(json.RawMessage(body))
	if err != nil {
		t.Fatalf("AddSet(%s): %v", body, err)
	}
	return set
}

func activeCount(keys []Key) int {
	n := 0
	for _, key := range keys {
		if key.RevokedAt == "" {
			n++
		}
	}
	return n
}

func TestSaveLoadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	store := openStore(t, dir)
	addServer(t, store, `{"name":"fs","command":["npx","-y","server-fs"]}`)
	addSet(t, store, `{"name":"demo","servers":["fs"],"port":9100}`)

	path := filepath.Join(dir, RegistryFile)
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("registry.json: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("registry.json perms = %o, want 600", perm)
	}
	bakInfo, err := os.Stat(path + ".bak")
	if err != nil {
		t.Fatalf("registry.json.bak: %v", err)
	}
	if perm := bakInfo.Mode().Perm(); perm != 0o600 {
		t.Errorf(".bak perms = %o, want 600", perm)
	}
	if err := store.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	reopened := openStore(t, dir)
	if reopened.Server("fs") == nil {
		t.Fatal("сервер fs не пережил перезагрузку")
	}
	set := reopened.Set("demo")
	if set == nil {
		t.Fatal("набор demo не пережил перезагрузку")
	}
	if set.Port != 9100 || len(set.Servers) != 1 || set.Servers[0] != "fs" {
		t.Errorf("набор demo после загрузки: %+v", set)
	}
	if len(set.Keys) != 1 || set.Token != set.Keys[0].Token {
		t.Errorf("ключ набора demo после загрузки: keys=%v token=%q", set.Keys, set.Token)
	}
}

func TestCorruptRegistryStartsEmpty(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, RegistryFile)
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	store := openStore(t, dir)
	if len(store.Servers()) != 0 || len(store.Sets()) != 0 {
		t.Errorf("после битого файла реестр не пуст: %v / %v", store.Servers(), store.Sets())
	}
	matches, err := filepath.Glob(path + ".corrupt-*")
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 1 {
		t.Fatalf("отложенных копий = %d, want 1", len(matches))
	}
	if _, err := os.Stat(path); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("registry.json остался на месте после битого файла")
	}
}

func TestKeysSingleActive(t *testing.T) {
	dir := t.TempDir()
	store := openStore(t, dir)
	addServer(t, store, `{"name":"fs","command":["npx","x"]}`)
	first := addSet(t, store, `{"name":"demo","servers":["fs"],"port":9100}`)
	if len(first.Keys) != 1 || len(first.Token) != 48 || first.Token != first.Keys[0].Token {
		t.Fatalf("первый ключ: %+v", first)
	}

	plain, err := store.IssueKey("demo")
	if err != nil {
		t.Fatalf("IssueKey: %v", err)
	}
	if plain == first.Token || len(plain) != 48 {
		t.Errorf("новый токен = %q", plain)
	}
	set := store.Set("demo")
	if len(set.Keys) != 2 || activeCount(set.Keys) != 1 {
		t.Fatalf("после выдачи ключей: %d, активных %d", len(set.Keys), activeCount(set.Keys))
	}
	if set.Keys[0].RevokedAt == "" {
		t.Error("прежний ключ не отозван")
	}
	if set.Token != plain {
		t.Errorf("token = %q, want зеркало активного %q", set.Token, plain)
	}

	wasActive, err := store.RevokeKey("demo", set.Keys[1].ID)
	if err != nil || !wasActive {
		t.Fatalf("RevokeKey активного: wasActive=%v err=%v", wasActive, err)
	}
	reopened := openStore(t, dir)
	set = reopened.Set("demo")
	if set.Token != "" || activeCount(set.Keys) != 0 {
		t.Errorf("после отзыва активного: token=%q активных=%d", set.Token, activeCount(set.Keys))
	}
	if _, err := reopened.RevokeKey("demo", "нет-такого"); err == nil {
		t.Error("отзыв несуществующего ключа не вернул ошибку")
	}
}

func TestPortRange(t *testing.T) {
	store := openStore(t, t.TempDir())
	addServer(t, store, `{"name":"fs","command":["npx","x"]}`)

	if _, err := store.AddSet(json.RawMessage(`{"name":"high","servers":["fs"],"port":9200}`)); err == nil {
		t.Error("порт 9200 принят, а должен быть отвергнут")
	}
	if _, err := store.AddSet(json.RawMessage(`{"name":"low","servers":["fs"],"port":9099}`)); err == nil {
		t.Error("порт 9099 принят, а должен быть отвергнут")
	}
	addSet(t, store, `{"name":"demo","servers":["fs"],"port":9100}`)
	addSet(t, store, `{"name":"second","servers":["fs"],"port":9101}`)

	port, err := store.AllocatePort()
	if err != nil {
		t.Fatalf("AllocatePort: %v", err)
	}
	if port != 9102 {
		t.Errorf("AllocatePort = %d, want 9102 (9100 и 9101 заняты)", port)
	}

	// Занять остаток диапазона: свободных портов быть не должно.
	for p := 9102; p <= LastPort; p++ {
		addSet(t, store, `{"name":"fill`+strconv.Itoa(p)+`","servers":["fs"],"port":`+strconv.Itoa(p)+`}`)
	}
	if _, err := store.AllocatePort(); err == nil {
		t.Error("AllocatePort не вернул ошибку при полном диапазоне")
	}
}

func TestDeleteServerStripsSets(t *testing.T) {
	dir := t.TempDir()
	store := openStore(t, dir)
	addServer(t, store, `{"name":"a","command":["x"]}`)
	addServer(t, store, `{"name":"b","command":["y"]}`)
	addSet(t, store, `{"name":"demo","servers":["a","b"],"port":9100}`)

	if err := store.DeleteServer("a"); err != nil {
		t.Fatalf("DeleteServer: %v", err)
	}
	set := store.Set("demo")
	if len(set.Servers) != 1 || set.Servers[0] != "b" {
		t.Errorf("серверы набора после удаления: %v", set.Servers)
	}
	reopened := openStore(t, dir)
	if got := reopened.Set("demo").Servers; len(got) != 1 || got[0] != "b" {
		t.Errorf("после перезагрузки серверы набора: %v", got)
	}
	if err := store.DeleteServer("a"); err == nil {
		t.Error("повторное удаление не вернуло ошибку")
	}
}

func TestPublicSetHidesTokens(t *testing.T) {
	store := openStore(t, t.TempDir())
	addServer(t, store, `{"name":"fs","command":["npx","x"]}`)
	set := addSet(t, store, `{"name":"demo","servers":["fs"],"port":9100}`)
	token := set.Token

	public := PublicSet(store.Set("demo"))
	body, err := json.Marshal(public)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), token) {
		t.Errorf("PublicSet содержит сырой токен: %s", body)
	}
	if strings.Contains(string(body), `"token"`) {
		t.Errorf("PublicSet содержит поле token: %s", body)
	}
	if public.KeyPrefix != token[:8] {
		t.Errorf("keyPrefix = %q, want %q", public.KeyPrefix, token[:8])
	}
	if len(public.Keys) != 1 || public.Keys[0].ID == "" {
		t.Errorf("keys в PublicSet: %+v", public.Keys)
	}
}

func TestImport(t *testing.T) {
	store := openStore(t, t.TempDir())
	entries := map[string]json.RawMessage{
		"str":   json.RawMessage(`"npx -y server-a"`),
		"arr":   json.RawMessage(`["npx","-y","server-b"]`),
		"obj":   json.RawMessage(`{"command":["npx","x"],"env":{"A":"${VAR}"}}`),
		"num":   json.RawMessage(`{"command":["npx","x"],"env":{"A":1}}`),
		"junk":  json.RawMessage(`42`),
		"Upper": json.RawMessage(`{"command":["x"]}`),
	}
	added, skipped := store.Import(entries)

	if got, want := strings.Join(added, ","), "arr,obj,str"; got != want {
		t.Errorf("added = %q, want %q", got, want)
	}
	reasons := make(map[string]string, len(skipped))
	for _, skip := range skipped {
		reasons[skip.Name] = skip.Reason
	}
	if !strings.Contains(reasons["num"], "env.A") {
		t.Errorf("num: %q", reasons["num"])
	}
	if reasons["junk"] != "запись не объект" {
		t.Errorf("junk: %q", reasons["junk"])
	}
	if !strings.Contains(reasons["Upper"], "имя сервера") {
		t.Errorf("Upper: %q", reasons["Upper"])
	}
	if srv := store.Server("str"); srv == nil || len(srv.Command) != 3 || srv.Command[2] != "server-a" {
		t.Errorf("сервер str: %+v", srv)
	}

	// Повторный импорт ничего не добавляет: имена уже есть.
	added, skipped = store.Import(entries)
	if len(added) != 0 || len(skipped) != len(entries) {
		t.Errorf("повторный импорт: added=%v skipped=%d", added, len(skipped))
	}

	addSet(t, store, `{"name":"demo","servers":["str","obj"],"port":9100}`)
	exported, err := store.ExportSet("demo")
	if err != nil {
		t.Fatalf("ExportSet: %v", err)
	}
	body, err := json.Marshal(exported)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), `${VAR}`) {
		t.Errorf("экспорт раскрыл ссылку на переменную: %s", body)
	}
	if !strings.Contains(string(body), `"mcpServers"`) {
		t.Errorf("экспорт без mcpServers: %s", body)
	}
}

func TestSeedFillsEmptyRegistry(t *testing.T) {
	dir := t.TempDir()
	seedPath := filepath.Join(t.TempDir(), "seed.json")
	seed := `{"version":1,
"servers":[{"name":"fs","command":["npx","x"]}],
"sets":[{"id":"demo","name":"Demo","servers":["fs"],"port":9100},
        {"id":"pub","name":"Pub","servers":["fs"],"port":9101,"open":true}]}`
	if err := os.WriteFile(seedPath, []byte(seed), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MANAGER_SEED_FILE", seedPath)

	store := openStore(t, dir)
	demo := store.Set("demo")
	if demo == nil || len(demo.Keys) != 1 || len(demo.Token) != 48 {
		t.Fatalf("сид-набор demo: %+v", demo)
	}
	if pub := store.Set("pub"); pub == nil || len(pub.Keys) != 0 || pub.Token != "" || !pub.Open {
		t.Fatalf("сид-набор pub: %+v", pub)
	}
	token := demo.Token

	reopened := openStore(t, dir)
	if got := reopened.Set("demo"); got == nil || got.Token != token {
		t.Errorf("токен сида пересоздан: %+v", got)
	}
	if len(reopened.Sets()) != 2 {
		t.Errorf("наборов после перезагрузки: %d, want 2", len(reopened.Sets()))
	}
}

func TestServerValidation(t *testing.T) {
	cases := []struct {
		name string
		body string
		ok   bool
	}{
		{"stdio", `{"name":"a","command":["npx","x"]}`, true},
		{"command строкой", `{"name":"a","command":"npx -y x"}`, true},
		{"http", `{"name":"a","url":"http://127.0.0.1/mcp"}`, true},
		{"stdio без command", `{"name":"a","transport":"stdio"}`, false},
		{"http без url", `{"name":"a","transport":"http","description":"d"}`, false},
		{"command и url", `{"name":"a","command":["x"],"url":"http://x"}`, false},
		{"cwd на http", `{"name":"a","url":"http://x","cwd":"/tmp"}`, false},
		{"env числом", `{"name":"a","command":["x"],"env":{"A":1}}`, false},
		{"headers числом", `{"name":"a","url":"http://x","headers":{"A":1}}`, false},
		{"имя заглавными", `{"name":"A","command":["x"]}`, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := openStore(t, t.TempDir())
			_, err := store.AddServer(json.RawMessage(tc.body))
			if tc.ok && err != nil {
				t.Fatalf("ожидался успех, ошибка: %v", err)
			}
			if !tc.ok && err == nil {
				t.Fatal("ожидалась ошибка, сервер принят")
			}
		})
	}
}

func TestSetValidation(t *testing.T) {
	store := openStore(t, t.TempDir())
	addServer(t, store, `{"name":"fs","command":["npx","x"]}`)

	cases := []struct {
		name string
		body string
	}{
		{"без серверов", `{"name":"demo","servers":[],"port":9101}`},
		{"сервер не в реестре", `{"name":"demo","servers":["nope"],"port":9102}`},
		{"autostart не bool", `{"name":"demo","servers":["fs"],"port":9103,"autostart":"true"}`},
		{"open не bool", `{"name":"demo","servers":["fs"],"port":9104,"open":1}`},
		{"порт занят", `{"name":"demo","servers":["fs"],"port":9100}`},
	}
	addSet(t, store, `{"name":"taken","servers":["fs"],"port":9100}`)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := store.AddSet(json.RawMessage(tc.body)); err == nil {
				t.Fatal("ожидалась ошибка, набор принят")
			}
		})
	}

	set, err := store.UpdateSet("taken", json.RawMessage(`{"servers":["fs"],"description":"d"}`))
	if err != nil {
		t.Fatalf("UpdateSet: %v", err)
	}
	if set.ID != "taken" || set.Port != 9100 || len(set.Keys) != 1 {
		t.Errorf("UpdateSet потерял поля: %+v", set)
	}
}
