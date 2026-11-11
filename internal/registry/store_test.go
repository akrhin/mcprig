package registry

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// freePort ищет свободный порт диапазона наборов: 9100 может занимать живой
// стенд или другой пакет тестов.
func freePort(t *testing.T, skip ...int) int {
	t.Helper()
	for port := FirstPort; port <= LastPort; port++ {
		if slices.Contains(skip, port) {
			continue
		}
		ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
		if err != nil {
			continue
		}
		ln.Close()
		return port
	}
	t.Fatal("нет свободного порта в диапазоне наборов")
	return 0
}

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
	port := freePort(t)
	addSet(t, store, `{"name":"demo","servers":["fs"],"port":`+strconv.Itoa(port)+`}`)

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
	if set.Port != port || len(set.Servers) != 1 || set.Servers[0] != "fs" {
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
	first := addSet(t, store, `{"name":"demo","servers":["fs"],"port":`+strconv.Itoa(freePort(t))+`}`)
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
	first, second := freePort(t), 0
	second = freePort(t, first)
	addSet(t, store, `{"name":"demo","servers":["fs"],"port":`+strconv.Itoa(first)+`}`)
	addSet(t, store, `{"name":"second","servers":["fs"],"port":`+strconv.Itoa(second)+`}`)

	port, err := store.AllocatePort()
	if err != nil {
		t.Fatalf("AllocatePort: %v", err)
	}
	if port == first || port == second || port < FirstPort || port > LastPort {
		t.Errorf("AllocatePort = %d, ждали свободный порт вне %d и %d", port, first, second)
	}

	// Занять остаток диапазона: свободных портов быть не должно. Занятые в
	// системе порты пропускаем — их и AllocatePort не выдаст.
	for p := FirstPort; p <= LastPort; p++ {
		if p == first || p == second || portBusy(p) {
			continue
		}
		addSet(t, store, `{"name":"fill`+strconv.Itoa(p)+`","servers":["fs"],"port":`+strconv.Itoa(p)+`}`)
	}
	if _, err := store.AllocatePort(); err == nil {
		t.Error("AllocatePort не вернул ошибку при полном диапазоне")
	}
}

// TestAllocatePortSkipsBusyOSPort: порт, занятый в системе, не выдаётся, даже
// если в реестре он свободен.
func TestAllocatePortSkipsBusyOSPort(t *testing.T) {
	// 9100 занимает либо сам тест, либо чужой процесс (живой стенд) — в обоих
	// случаях AllocatePort не должен его выдать.
	if ln, err := net.Listen("tcp", "127.0.0.1:9100"); err == nil {
		defer ln.Close()
	}
	store := openStore(t, t.TempDir())
	port, err := store.AllocatePort()
	if err != nil {
		t.Fatalf("AllocatePort: %v", err)
	}
	if port == 9100 {
		t.Fatalf("AllocatePort выдал занятый 9100")
	}
}

func TestDeleteServerStripsSets(t *testing.T) {
	dir := t.TempDir()
	store := openStore(t, dir)
	addServer(t, store, `{"name":"a","command":["x"]}`)
	addServer(t, store, `{"name":"b","command":["y"]}`)
	addSet(t, store, `{"name":"demo","servers":["a","b"],"port":`+strconv.Itoa(freePort(t))+`}`)

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

func TestDeleteServerRefusesLastInSet(t *testing.T) {
	dir := t.TempDir()
	store := openStore(t, dir)
	addServer(t, store, `{"name":"a","command":["x"]}`)
	addServer(t, store, `{"name":"b","command":["y"]}`)
	addSet(t, store, `{"name":"demo","servers":["a"],"port":`+strconv.Itoa(freePort(t))+`}`)

	err := store.DeleteServer("a")
	if err == nil {
		t.Fatal("удаление последнего сервера набора не вернуло ошибку")
	}
	if !strings.Contains(err.Error(), "последний в наборе") {
		t.Errorf("ошибка: %q", err)
	}
	if store.Server("a") == nil {
		t.Error("сервер удалён, хотя операция должна была отказать")
	}
	if got := store.Set("demo").Servers; len(got) != 1 || got[0] != "a" {
		t.Errorf("набор после отказа: %v", got)
	}
}

func TestPublicSetHidesTokens(t *testing.T) {
	store := openStore(t, t.TempDir())
	addServer(t, store, `{"name":"fs","command":["npx","x"]}`)
	set := addSet(t, store, `{"name":"demo","servers":["fs"],"port":`+strconv.Itoa(freePort(t))+`}`)
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

	if got, want := strings.Join(added, ","), "arr,obj"; got != want {
		t.Errorf("added = %q, want %q", got, want)
	}
	reasons := make(map[string]string, len(skipped))
	for _, skip := range skipped {
		reasons[skip.Name] = skip.Reason
	}
	if reasons["str"] != "command: передайте массив" {
		t.Errorf("str: %q", reasons["str"])
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
	if srv := store.Server("str"); srv != nil {
		t.Errorf("строковая команда импортирована: %+v", srv)
	}

	// Повторный импорт ничего не добавляет: имена уже есть.
	added, skipped = store.Import(entries)
	if len(added) != 0 || len(skipped) != len(entries) {
		t.Errorf("повторный импорт: added=%v skipped=%d", added, len(skipped))
	}

	addSet(t, store, `{"name":"demo","servers":["arr","obj"],"port":`+strconv.Itoa(freePort(t))+`}`)
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
		want string // "" — сервер должен приняться
	}{
		{"stdio", `{"name":"a","command":["npx","x"]}`, ""},
		{"command строкой", `{"name":"a","command":"npx -y x"}`, ""},
		{"http", `{"name":"a","url":"http://127.0.0.1/mcp"}`, ""},
		{"stdio без command", `{"name":"a","transport":"stdio"}`, "нужен command[] или url"},
		{"http без url", `{"name":"a","transport":"http","description":"d"}`, "нужен command[] или url"},
		{"command и url", `{"name":"a","command":["x"],"url":"http://x"}`, "что-то одно"},
		{"cwd на http", `{"name":"a","url":"http://x","cwd":"/tmp"}`, "cwd допустим только"},
		{"env числом", `{"name":"a","command":["x"],"env":{"A":1}}`, "env.A: ожидалась строка"},
		{"headers числом", `{"name":"a","url":"http://x","headers":{"A":1}}`, "headers.A: ожидалась строка"},
		{"имя заглавными", `{"name":"A","command":["x"]}`, "имя сервера"},
		{"enabled строкой", `{"name":"a","command":["x"],"enabled":"yes"}`, "enabled: ожидалось true или false"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := openStore(t, t.TempDir())
			_, err := store.AddServer(json.RawMessage(tc.body))
			if tc.want == "" {
				if err != nil {
					t.Fatalf("ожидался успех, ошибка: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("ожидалась ошибка с %q, сервер принят", tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("ошибка %q не содержит %q", err, tc.want)
			}
		})
	}
}

// A string command names the executable; splitting it into argv is MCPHub
// import shorthand only.
func TestCommandStringIsOneArgument(t *testing.T) {
	store := openStore(t, t.TempDir())
	str := addServer(t, store, `{"name":"str","command":"npx -y pkg"}`)
	if len(str.Command) != 1 || str.Command[0] != "npx -y pkg" {
		t.Fatalf("command строкой: want [\"npx -y pkg\"], got %v", str.Command)
	}
	arr := addServer(t, store, `{"name":"arr","command":["npx","-y","pkg"]}`)
	if len(arr.Command) != 3 || arr.Command[2] != "pkg" {
		t.Fatalf("command массивом: want [npx -y pkg], got %v", arr.Command)
	}
}

// Merging a patch rebuilds the entry from its marshalled form; enabled:false
// must survive both a patch without the field and an explicit false.
func TestUpdateKeepsEnabled(t *testing.T) {
	dir := t.TempDir()
	store := openStore(t, dir)
	addServer(t, store, `{"name":"off","command":["x"],"enabled":false}`)
	addServer(t, store, `{"name":"on","command":["x"]}`)

	srv, err := store.UpdateServer("off", json.RawMessage(`{"description":"d"}`))
	if err != nil {
		t.Fatalf("UpdateServer(off): %v", err)
	}
	if srv.Enabled {
		t.Fatal("патч без enabled включил выключенный сервер")
	}
	if on := store.Server("on"); on == nil || !on.Enabled {
		t.Fatal("create без enabled должен давать true")
	}
	if srv, err = store.UpdateServer("on", json.RawMessage(`{"enabled":false}`)); err != nil || srv.Enabled {
		t.Fatalf("явный enabled:false не применён: %+v %v", srv, err)
	}
	if srv, err = store.UpdateServer("on", json.RawMessage(`{"description":"e"}`)); err != nil || srv.Enabled {
		t.Fatalf("патч без enabled включил сервер обратно: %+v %v", srv, err)
	}

	reopened := openStore(t, dir)
	if got := reopened.Server("off"); got == nil || got.Enabled {
		t.Errorf("enabled:false не пережил перезагрузку: %+v", got)
	}
}

// CanServe is the single rule for "this set may be started".
func TestCanServe(t *testing.T) {
	store := openStore(t, t.TempDir())
	addServer(t, store, `{"name":"fs","command":["x"]}`)
	keyedPort := freePort(t)
	openPort := freePort(t, keyedPort)
	keyed := addSet(t, store, `{"name":"keyed","servers":["fs"],"port":`+strconv.Itoa(keyedPort)+`}`)
	open := addSet(t, store, `{"name":"open","servers":["fs"],"port":`+strconv.Itoa(openPort)+`,"open":true}`)
	if !CanServe(keyed) || !CanServe(open) {
		t.Fatal("набор с активным ключом или open должен стартовать")
	}
	if CanServe(&Set{Name: "bare"}) || CanServe(nil) {
		t.Fatal("набор без ключа и не open стартовать не должен")
	}
}

// A failed write must leave memory as it was: memory never runs ahead of disk.
func TestFailedWriteKeepsMemory(t *testing.T) {
	// root игнорирует права каталога: проверка возможна только от обычного uid.
	if os.Geteuid() == 0 {
		t.Skip("root не подчиняется правам каталога")
	}
	dir := t.TempDir()
	store := openStore(t, dir)
	addServer(t, store, `{"name":"keep","command":["x"]}`)
	servers, sets := len(store.Servers()), len(store.Sets())

	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })

	if _, err := store.AddServer(json.RawMessage(`{"name":"lost","command":["x"]}`)); err == nil {
		t.Fatal("запись в каталог только на чтение должна падать")
	}
	if _, err := store.AddSet(json.RawMessage(`{"name":"lost","servers":["keep"],"port":` + strconv.Itoa(freePort(t)) + `}`)); err == nil {
		t.Fatal("запись набора в каталог только на чтение должна падать")
	}
	if added, skipped := store.Import(map[string]json.RawMessage{
		"imp": json.RawMessage(`{"command":["x"]}`),
	}); added != nil || len(skipped) == 0 {
		t.Fatalf("Import при ошибке записи: added %v, skipped %v", added, skipped)
	}
	if len(store.Servers()) != servers || len(store.Sets()) != sets {
		t.Fatalf("после ошибки записи состояние изменилось: серверов %d/%d, наборов %d/%d",
			len(store.Servers()), servers, len(store.Sets()), sets)
	}
}

// Sets()/Set() hand out snapshots: writing through them cannot reach the store.
func TestSetSnapshots(t *testing.T) {
	store := openStore(t, t.TempDir())
	addServer(t, store, `{"name":"fs","command":["x"]}`)
	addSet(t, store, `{"name":"demo","servers":["fs"],"port":`+strconv.Itoa(freePort(t))+`}`)

	got := store.Set("demo")
	got.Name = "hacked"
	got.Servers[0] = "gone"
	got.Keys[0].Token = "leak"
	for _, set := range store.Sets() {
		set.Name = "list-hacked"
	}
	kept := store.Set("demo")
	if kept.Name != "demo" || kept.Servers[0] != "fs" || kept.Token == "leak" {
		t.Fatalf("запись через снапшот изменила реестр: %+v", kept)
	}
}

func TestSetValidation(t *testing.T) {
	store := openStore(t, t.TempDir())
	addServer(t, store, `{"name":"fs","command":["npx","x"]}`)

	taken := freePort(t)
	cases := []struct {
		name string
		body string
	}{
		{"без серверов", `{"name":"demo","servers":[]}`},
		{"сервер не в реестре", `{"name":"demo","servers":["nope"]}`},
		{"autostart не bool", `{"name":"demo","servers":["fs"],"autostart":"true"}`},
		{"open не bool", `{"name":"demo","servers":["fs"],"open":1}`},
		{"порт занят", `{"name":"demo","servers":["fs"],"port":` + strconv.Itoa(taken) + `}`},
	}
	addSet(t, store, `{"name":"taken","servers":["fs"],"port":`+strconv.Itoa(taken)+`}`)
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
	if set.ID != "taken" || set.Port != taken || len(set.Keys) != 1 {
		t.Errorf("UpdateSet потерял поля: %+v", set)
	}
}

// id набора уходит в путь sets/<id>/servers.json, поэтому загрузка обязана
// отбрасывать id вне шаблона, не роняя остальные записи.
func TestLoadDropsSetWithBadID(t *testing.T) {
	dir := t.TempDir()
	body := `{"version":1,"nextPort":9100,"servers":[{"name":"fs","command":["x"]}],"sets":[
		{"id":"ok","name":"ok","servers":["fs"],"port":9100,"keys":[{"id":"k1","token":"t1"},{"id":"../k","token":"t2"}]},
		{"id":"../evil","name":"evil","servers":["fs"],"port":9101,"keys":[{"id":"k2","token":"t3"}]}
	]}`
	if err := os.WriteFile(filepath.Join(dir, RegistryFile), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	store := openStore(t, dir)

	ok := store.Set("ok")
	if ok == nil {
		t.Fatal("целый набор ok не загрузился")
	}
	if len(ok.Keys) != 1 || ok.Keys[0].ID != "k1" {
		t.Errorf("ключи ok = %+v, want только k1", ok.Keys)
	}
	if store.Set("../evil") != nil {
		t.Error("набор с id ../evil не отброшен")
	}
	if len(store.Sets()) != 1 {
		t.Errorf("наборов = %d, want 1", len(store.Sets()))
	}
	if dropped := store.Dropped(); len(dropped) != 1 || !strings.Contains(dropped[0], "../evil") {
		t.Errorf("отчёт о загрузке = %v", dropped)
	}
}

func TestKeyOpsMissingSet(t *testing.T) {
	store := openStore(t, t.TempDir())
	if _, err := store.IssueKey("nope"); err == nil || !strings.Contains(err.Error(), "нет набора") {
		t.Errorf("IssueKey: err = %v", err)
	}
	if _, err := store.RevokeKey("nope", "k"); err == nil || !strings.Contains(err.Error(), "нет набора") {
		t.Errorf("RevokeKey: err = %v", err)
	}
}
