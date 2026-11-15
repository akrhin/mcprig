package mcpserver

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/akrhin/mcprig/internal/config"
)

// reloadCfg is a set config with the given servers, nothing else.
func reloadCfg(servers ...*config.Server) *config.Config {
	return &config.Config{Servers: servers}
}

// sessionOf is the session the entry currently holds, nil before the first call.
func sessionOf(e *entry) *mcp.ClientSession {
	e.conn.mu.Lock()
	defer e.conn.mu.Unlock()
	return e.conn.sess
}

// Новый сервер в конфиге появляется в наборе без соединения: сессия
// поднимается первым вызовом, как при старте.
func TestReloadAddsServerLazily(t *testing.T) {
	var dials int32
	m := testManager(reloadCfg(), &dials, 0)
	defer m.Close()
	cs := clientFor(t, m.Server())

	res := m.Reload(reloadCfg(stdio("beta")))
	if res.Generation != 1 {
		t.Fatalf("generation = %d, want 1", res.Generation)
	}
	if len(res.Applied) != 1 || res.Applied[0] != "added:beta" {
		t.Fatalf("applied = %v", res.Applied)
	}
	if got := atomic.LoadInt32(&dials); got != 0 {
		t.Fatalf("добавление сервера соединилось сразу: %d подключений", got)
	}
	cmds, err := m.commandsOf(context.Background(), "beta")
	if err != nil {
		t.Fatalf("commandsOf: %v", err)
	}
	if len(cmds) == 0 {
		t.Fatal("у добавленного сервера нет инструментов")
	}
	if got := atomic.LoadInt32(&dials); got != 1 {
		t.Fatalf("первый вызов дал %d подключений, want 1", got)
	}
	// list_commands видит добавленный сервер и через MCP-границу.
	if out := listServersOut(t, cs); len(out.Servers) != 1 || out.Servers[0].Name != "beta" {
		t.Fatalf("list_servers после reload: %+v", out.Servers)
	}
}

// Удалённый сервер теряет сессию и кэш инструментов.
func TestReloadRemovesServerClosesSession(t *testing.T) {
	var dials int32
	m := testManager(reloadCfg(stdio("alpha"), stdio("beta")), &dials, 0)
	defer m.Close()
	cs := clientFor(t, m.Server())
	if res := call(t, cs, "invoke_command", echoCall("alpha", "раз", 0)); res.IsError {
		t.Fatalf("вызов до reload: %s", resultText(t, res))
	}
	e := m.find("alpha")
	if e == nil {
		t.Fatal("сервер alpha не найден")
	}
	if _, err := m.commandsOf(context.Background(), "alpha"); err != nil {
		t.Fatalf("commandsOf: %v", err)
	}
	if len(e.cachedTools()) == 0 {
		t.Fatal("кэш инструментов alpha не наполнен")
	}
	sess := sessionOf(e)

	res := m.Reload(reloadCfg(stdio("beta")))
	if len(res.Applied) != 1 || res.Applied[0] != "removed:alpha" {
		t.Fatalf("applied = %v", res.Applied)
	}
	if m.find("alpha") != nil {
		t.Fatal("удалённый сервер остался в наборе")
	}
	// Сессия отброшенной записи закрывается после ack — ждём её горутину.
	m.closing.Wait()
	if e.cachedTools() != nil {
		t.Fatal("кэш инструментов удалённого сервера не сброшен")
	}
	if sess != nil {
		if _, err := sess.CallTool(context.Background(), &mcp.CallToolParams{Name: "echo"}); err == nil {
			t.Fatal("сессия удалённого сервера не закрыта")
		}
	}
	res2 := call(t, cs, "invoke_command", echoCall("alpha", "два", 0))
	if !res2.IsError {
		t.Fatal("вызов удалённого сервера прошёл")
	}
}

// Правка command закрывает сессию: следующий вызов идёт на новом сервере.
func TestReloadChangedCommandResetsSession(t *testing.T) {
	var dials int32
	first := stdio("alpha")
	first.Command = []string{"downstream", "--one"}
	m := testManager(reloadCfg(first), &dials, 0)
	defer m.Close()
	cs := clientFor(t, m.Server())
	if res := call(t, cs, "invoke_command", echoCall("alpha", "раз", 0)); res.IsError {
		t.Fatalf("вызов до reload: %s", resultText(t, res))
	}
	oldEntry := m.find("alpha")
	oldSess := sessionOf(oldEntry)
	if oldSess == nil {
		t.Fatal("сессия до reload не поднята")
	}

	second := stdio("alpha")
	second.Command = []string{"downstream", "--two"}
	res := m.Reload(reloadCfg(second))
	if len(res.Applied) != 1 || res.Applied[0] != "reset:alpha" {
		t.Fatalf("applied = %v", res.Applied)
	}
	if got := atomic.LoadInt32(&dials); got != 1 {
		t.Fatalf("reload сам поднял сессию: %d подключений", got)
	}
	// Сессия отброшенной записи закрывается после ack — ждём её горутину.
	m.closing.Wait()
	if _, err := oldSess.CallTool(context.Background(), &mcp.CallToolParams{Name: "echo"}); err == nil {
		t.Fatal("старая сессия не закрыта")
	}
	fresh := m.find("alpha")
	if fresh == nil || fresh == oldEntry {
		t.Fatal("после правки команды нужна новая запись сервера")
	}
	if got := fresh.server().Command; len(got) != 2 || got[1] != "--two" {
		t.Fatalf("новый command: %v", got)
	}

	if res := call(t, cs, "invoke_command", echoCall("alpha", "два", 0)); res.IsError {
		t.Fatalf("вызов после reload: %s", resultText(t, res))
	}
	if got := atomic.LoadInt32(&dials); got != 2 {
		t.Fatalf("после reload ожидалось 2 подключения, получено %d", got)
	}
}

// Правка одного сервера не трогает соседа: та же запись, та же сессия, тот же кэш.
func TestReloadKeepsUnchangedSessions(t *testing.T) {
	var dials int32
	alpha := stdio("alpha")
	beta := stdio("beta")
	m := testManager(reloadCfg(alpha, beta), &dials, 0)
	defer m.Close()
	cs := clientFor(t, m.Server())
	if res := call(t, cs, "invoke_command", echoCall("alpha", "раз", 0)); res.IsError {
		t.Fatalf("вызов alpha: %s", resultText(t, res))
	}
	kept := m.find("alpha")
	keptSess := sessionOf(kept)
	keptTools := kept.cachedTools()

	edited := stdio("beta")
	edited.Description = "новое описание"
	res := m.Reload(reloadCfg(alpha, edited))
	if len(res.Applied) != 1 || res.Applied[0] != "updated:beta" {
		t.Fatalf("applied = %v", res.Applied)
	}
	if m.find("alpha") != kept {
		t.Fatal("запись неизменённого сервера подменена")
	}
	if sessionOf(kept) != keptSess {
		t.Fatal("сессия неизменённого сервера пересоздана")
	}
	if len(kept.cachedTools()) != len(keptTools) {
		t.Fatal("кэш инструментов неизменённого сервера сброшен")
	}
	if got := m.find("beta").server().Description; got != "новое описание" {
		t.Fatalf("описание beta: %q", got)
	}
	if got := atomic.LoadInt32(&dials); got != 1 {
		t.Fatalf("reload переподключил кого-то: %d подключений", got)
	}
}

// Битый набор не роняет процесс: прежний конфиг продолжает работать.
func TestReloadRejectsBrokenConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "servers.json")
	body := `{"servers": [{"name": "alpha", "command": ["downstream"], "enabled": true}]}`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	var dials int32
	m := testManager(cfg, &dials, 0)
	defer m.Close()
	cs := clientFor(t, m.Server())
	if res := call(t, cs, "invoke_command", echoCall("alpha", "раз", 0)); res.IsError {
		t.Fatalf("вызов до reload: %s", resultText(t, res))
	}

	if err := os.WriteFile(path, []byte(`{"servers": [`), 0o600); err != nil {
		t.Fatal(err)
	}
	_, loadErr := config.Load(path)
	if loadErr == nil {
		t.Fatal("битый набор прочитался")
	}
	res := m.ReloadFailed(loadErr)
	if res.Err == "" {
		t.Fatal("ошибка перечитки не попала в результат")
	}
	if res.Generation != 1 {
		t.Fatalf("generation = %d, want 1", res.Generation)
	}
	if len(res.Applied) != 0 {
		t.Fatalf("битый конфиг что-то применил: %v", res.Applied)
	}

	if res := call(t, cs, "invoke_command", echoCall("alpha", "два", 0)); res.IsError {
		t.Fatalf("вызов после неудачной перечитки: %s", resultText(t, res))
	}
	if got := atomic.LoadInt32(&dials); got != 1 {
		t.Fatalf("сессия потеряна на неудачной перечитке: %d подключений", got)
	}
}

// Новый rateLimit и новые permissions применяются на месте, без перезапуска.
func TestReloadAppliesRateLimitAndPermissions(t *testing.T) {
	var dials int32
	alpha := stdio("alpha")
	m := testManager(reloadCfg(alpha), &dials, 0)
	defer m.Close()
	cs := clientFor(t, m.Server())
	if res := call(t, cs, "invoke_command", echoCall("alpha", "раз", 0)); res.IsError {
		t.Fatalf("вызов до reload: %s", resultText(t, res))
	}
	if res := call(t, cs, "invoke_command", map[string]any{
		"server": "alpha", "command_name": "danger-zone", "parameters": map[string]any{},
	}); res.IsError {
		t.Fatalf("danger-zone до reload: %s", resultText(t, res))
	}

	edited := stdio("alpha")
	edited.Permissions = &config.Permissions{Rules: []config.Rule{{Pattern: "danger-*", Action: config.PermissionDeny}}}
	next := reloadCfg(edited)
	next.RateLimit = &config.RateLimit{PerMinute: 60, Burst: 1}
	if res := m.Reload(next); len(res.Applied) != 2 {
		t.Fatalf("applied = %v", res.Applied)
	}

	blocked := call(t, cs, "invoke_command", map[string]any{
		"server": "alpha", "command_name": "danger-zone", "parameters": map[string]any{},
	})
	if !blocked.IsError {
		t.Fatal("новое правило не запретило danger-zone")
	}
	if res := call(t, cs, "invoke_command", echoCall("alpha", "первый", 0)); res.IsError {
		t.Fatalf("первый вызов под лимитом: %s", resultText(t, res))
	}
	over := call(t, cs, "invoke_command", echoCall("alpha", "второй", 0))
	if !over.IsError {
		t.Fatal("новый лимит не отказал")
	}
	if retryOf(t, over) <= 0 {
		t.Fatal("отказ лимита без задержки")
	}
}

// Поля транспорта, которые нельзя применить на живом процессе, честно
// называются и остаются прежними: прежний адрес продолжает работать.
func TestReloadTransportNeedsRestart(t *testing.T) {
	var dials int32
	m := testManager(&config.Config{
		Servers:   []*config.Server{stdio("alpha")},
		Transport: &config.HTTPTransport{Type: "http", AuthToken: "old-token", Host: "127.0.0.1"},
	}, &dials, 0)
	defer m.Close()

	res := m.Reload(&config.Config{
		Servers:   []*config.Server{stdio("alpha")},
		Transport: &config.HTTPTransport{Type: "http", AuthToken: "old-token", Host: "0.0.0.0"},
	})
	if len(res.RestartNeeded) != 1 || res.RestartNeeded[0] != "host" {
		t.Fatalf("restartNeeded = %v", res.RestartNeeded)
	}
	if got := m.config().Transport.Host; got != "127.0.0.1" {
		t.Fatalf("менеджер принял новый адрес без перезапуска: %q", got)
	}
	if res.Err != "" {
		t.Fatalf("err = %q", res.Err)
	}
}

// Смена ключа перезапуска не требует: токены ставит живой слушатель, а
// менеджер обязан принять новое поколение целиком — иначе панель получает
// ложный restartNeeded, а профили ключей откатываются к прежним.
func TestReloadTokenAppliesWithoutRestart(t *testing.T) {
	var dials int32
	m := testManager(&config.Config{
		Servers: []*config.Server{stdio("alpha")},
		Transport: &config.HTTPTransport{Type: "http", AuthToken: "old-token",
			KeyProfiles: map[string]string{"aaaaaa": "reader"}},
		Profiles: map[string]*config.Profile{"reader": {Allow: []string{"alpha"}}},
	}, &dials, 0)
	defer m.Close()

	res := m.Reload(&config.Config{
		Servers: []*config.Server{stdio("alpha")},
		Transport: &config.HTTPTransport{Type: "http", AuthToken: "new-token",
			KeyProfiles: map[string]string{"bbbbbb": "reader"}},
		Profiles: map[string]*config.Profile{"reader": {Allow: []string{"alpha"}}},
	})
	if len(res.RestartNeeded) != 0 {
		t.Fatalf("restartNeeded = %v", res.RestartNeeded)
	}
	if res.Err != "" {
		t.Fatalf("err = %q", res.Err)
	}
	cfg := m.config().Transport
	if cfg.AuthToken != "new-token" {
		t.Fatalf("токен не применён: %q", cfg.AuthToken)
	}
	if cfg.KeyProfiles["bbbbbb"] != "reader" {
		t.Fatalf("профиль нового ключа потерян: %v", cfg.KeyProfiles)
	}
}

// Перезагрузка во время вызовов: -race ловит гонку настроек, замок не должен
// ни держать сетевой вызов, ни терять сессию под ногами у вызова.
func TestReloadDuringCallsIsRaceFree(t *testing.T) {
	var dials int32
	m := testManager(reloadCfg(stdio("alpha")), &dials, 0)
	defer m.Close()
	cs := clientFor(t, m.Server())

	var wg sync.WaitGroup
	stop := make(chan struct{})
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				_, _ = cs.CallTool(context.Background(), &mcp.CallToolParams{
					Name: "invoke_command", Arguments: echoCall("alpha", "x", 0),
				})
				_, _ = cs.CallTool(context.Background(), &mcp.CallToolParams{
					Name: "list_commands", Arguments: map[string]any{"server": "alpha"},
				})
			}
		}()
	}
	for i := 0; i < 6; i++ {
		edited := stdio("alpha")
		edited.Tags = []string{fmt.Sprint(i)}
		if i%3 == 2 {
			// Половина попыток закрывает сессию под работающим вызовом.
			edited.Command = []string{"downstream", fmt.Sprint(i)}
		}
		m.Reload(reloadCfg(edited))
	}
	close(stop)
	wg.Wait()
}

// Перезагрузка, не трогающая rateLimit, не обнуляет его счётчики: иначе
// лимит обходится вызовом reload.
func TestReloadKeepsRateLimitCounters(t *testing.T) {
	var dials int32
	m := testManager(&config.Config{
		Servers:   []*config.Server{stdio("alpha")},
		RateLimit: &config.RateLimit{PerMinute: 60, Burst: 1},
	}, &dials, 0)
	defer m.Close()
	cs := clientFor(t, m.Server())
	if res := call(t, cs, "invoke_command", echoCall("alpha", "первый", 0)); res.IsError {
		t.Fatalf("первый вызов: %s", resultText(t, res))
	}
	if res := m.Reload(&config.Config{
		Servers:   []*config.Server{stdio("alpha")},
		RateLimit: &config.RateLimit{PerMinute: 60, Burst: 1},
	}); len(res.Applied) != 0 {
		t.Fatalf("перезагрузка без правок что-то применила: %v", res.Applied)
	}
	if res := call(t, cs, "invoke_command", echoCall("alpha", "второй", 0)); !res.IsError {
		t.Fatal("перезагрузка обнулила счётчик лимита")
	}
}

// Отброшенная запись не подключается заново по старому конфигу: вызов, который
// нашёл её до перезагрузки, получает отказ, а не второй dial старого процесса.
func TestReloadRetiresDroppedEntry(t *testing.T) {
	var dials int32
	m := testManager(reloadCfg(stdio("alpha")), &dials, 0)
	defer m.Close()
	cs := clientFor(t, m.Server())
	if res := call(t, cs, "invoke_command", echoCall("alpha", "x", 0)); res.IsError {
		t.Fatalf("первый вызов: %s", resultText(t, res))
	}
	stale := m.find("alpha")
	if sessionOf(stale) == nil {
		t.Fatal("первый вызов не поднял сессию")
	}

	edited := stdio("alpha")
	edited.Command = []string{"downstream", "new"}
	if res := m.Reload(reloadCfg(edited)); len(res.Applied) != 1 || res.Applied[0] != "reset:alpha" {
		t.Fatalf("applied = %v", res.Applied)
	}
	m.closing.Wait()

	if err := stale.conn.stopErr(); err != errRetired {
		t.Fatalf("отброшенная запись: %v, want errRetired", err)
	}
	if sessionOf(stale) != nil {
		t.Fatal("старая сессия осталась открытой")
	}
	if _, err := m.callTool(context.Background(), stale, &mcp.CallToolParams{Name: "echo"}); !errors.Is(err, errRetired) {
		t.Fatalf("вызов на отброшенной записи: %v, want errRetired", err)
	}
	if got := atomic.LoadInt32(&dials); got != 1 {
		t.Fatalf("отброшенная запись соединилась заново: %d подключений", got)
	}

	// Следующий вызов уходит на новую запись и поднимает новое соединение.
	if res := call(t, cs, "invoke_command", echoCall("alpha", "x", 0)); res.IsError {
		t.Fatalf("вызов после перезагрузки: %s", resultText(t, res))
	}
	if fresh := m.find("alpha"); fresh == stale {
		t.Fatal("запись не подменена")
	}
	if got := atomic.LoadInt32(&dials); got != 2 {
		t.Fatalf("подключений %d, want 2 (новое после перезагрузки)", got)
	}
}

// Подключение в полёте: ack не ждёт его завершения, а после перезагрузки
// старый конфиг не даёт ни одного нового dial.
func TestReloadAckDoesNotWaitForPendingConnect(t *testing.T) {
	var dials int32
	m := New(reloadCfg(stdio("alpha")), "test", nil)
	defer m.Close()
	started := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	m.dial = func(ctx context.Context, srv *config.Server) (*mcp.ClientSession, error) {
		atomic.AddInt32(&dials, 1)
		once.Do(func() { close(started) })
		<-release
		return upstream(ctx)
	}
	cs := clientFor(t, m.Server())

	done := make(chan *mcp.CallToolResult, 1)
	go func() { done <- call(t, cs, "invoke_command", echoCall("alpha", "x", 0)) }()
	<-started

	edited := stdio("alpha")
	edited.Command = []string{"downstream", "new"}
	start := time.Now()
	if res := m.Reload(reloadCfg(edited)); len(res.Applied) != 1 {
		t.Fatalf("applied = %v", res.Applied)
	}
	if waited := time.Since(start); waited > time.Second {
		t.Fatalf("ack ждал подключение %s", waited)
	}

	close(release)
	res := <-done
	if !strings.Contains(resultText(t, res), "перезагрузкой") {
		t.Fatalf("вызов в полёте: %s", resultText(t, res))
	}
	m.closing.Wait()
	if got := atomic.LoadInt32(&dials); got != 1 {
		t.Fatalf("подключений %d, want 1: старый конфиг больше не диалует", got)
	}
	if res := call(t, cs, "invoke_command", echoCall("alpha", "x", 0)); res.IsError {
		t.Fatalf("вызов после перезагрузки: %s", resultText(t, res))
	}
	if got := atomic.LoadInt32(&dials); got != 2 {
		t.Fatalf("подключений %d, want 2", got)
	}
}

// Отвергнутая смена адреса не снимает профили: ключ, у которого профиль пропал
// из нового файла, иначе остался бы при прежнем токене без ограничений.
func TestReloadKeepsProfilesWhenTokenRejected(t *testing.T) {
	var dials int32
	m := testManager(&config.Config{
		Servers: []*config.Server{stdio("alpha")},
		Transport: &config.HTTPTransport{
			Type: "http", AuthToken: "old-token", Host: "127.0.0.1",
			KeyProfiles: map[string]string{"fp1": "reader"},
		},
	}, &dials, 0)
	defer m.Close()

	res := m.Reload(&config.Config{
		Servers:   []*config.Server{stdio("alpha")},
		Transport: &config.HTTPTransport{Type: "http", AuthToken: "new-token", Host: "0.0.0.0"},
	})
	if len(res.RestartNeeded) != 1 || res.RestartNeeded[0] != "host" {
		t.Fatalf("restartNeeded = %v", res.RestartNeeded)
	}
	tr := m.config().Transport
	if tr.AuthToken != "old-token" {
		t.Fatalf("токен = %q, want прежний", tr.AuthToken)
	}
	if tr.KeyProfiles["fp1"] != "reader" {
		t.Fatalf("профиль исчез из живого конфига: %v", tr.KeyProfiles)
	}
}

// Блок транспорта, исчезнувший из файла (или появившийся в нём), на месте не
// применяется: слушатель построен при старте, а пустой транспорт снял бы
// профили и открыл набор.
func TestReloadKeepsTransportWhenBlockChangesPresence(t *testing.T) {
	var dials int32
	m := testManager(&config.Config{
		Servers:   []*config.Server{stdio("alpha")},
		Transport: &config.HTTPTransport{Type: "http", AuthToken: "old-token"},
	}, &dials, 0)
	defer m.Close()

	res := m.Reload(&config.Config{Servers: []*config.Server{stdio("alpha")}})
	if len(res.RestartNeeded) != 1 || res.RestartNeeded[0] != "transport" {
		t.Fatalf("удаление блока: restartNeeded = %v", res.RestartNeeded)
	}
	if tr := m.config().Transport; tr == nil || tr.AuthToken != "old-token" {
		t.Fatalf("живой транспорт потерян: %+v", tr)
	}

	// Обратный случай: транспорт появился в файле, у процесса его не было.
	plain := testManager(reloadCfg(stdio("alpha")), &dials, 0)
	defer plain.Close()
	res = plain.Reload(&config.Config{
		Servers:   []*config.Server{stdio("alpha")},
		Transport: &config.HTTPTransport{Type: "http", AuthToken: "new-token"},
	})
	if len(res.RestartNeeded) != 1 || res.RestartNeeded[0] != "transport" {
		t.Fatalf("появление блока: restartNeeded = %v", res.RestartNeeded)
	}
	if tr := plain.config().Transport; tr != nil {
		t.Fatalf("менеджер принял транспорт без перезапуска: %+v", tr)
	}
}

// Слоты ключуются именем сервера: запись, подменённая перезагрузкой, делит
// лимит с прежней, а не удваивает его.
func TestSlotsSharedAcrossReset(t *testing.T) {
	var dials int32
	m := testManager(reloadCfg(stdio("alpha")), &dials, 0)
	defer m.Close()
	stale := m.find("alpha")

	edited := stdio("alpha")
	edited.Command = []string{"downstream", "new"}
	m.Reload(reloadCfg(edited))
	fresh := m.find("alpha")
	if fresh == stale {
		t.Fatal("запись не подменена")
	}

	slot := m.slots.get(stale.server().Name)
	for i := 0; i < m.slots.limit; i++ {
		slot <- struct{}{}
	}
	defer func() {
		for i := 0; i < m.slots.limit; i++ {
			<-slot
		}
	}()
	select {
	case m.slots.get(fresh.server().Name) <- struct{}{}:
		t.Fatal("после reset у сервера свой слот: лимит удвоился")
	default:
	}
	if n := len(m.slots.byKey); n != 1 {
		t.Fatalf("карта слотов выросла: %d", n)
	}
}

// Перезагрузка после Close ничего не применяет: иначе она подменила бы записи
// у уже снятого снимка и отброшенные сессии остались бы открытыми.
func TestReloadAfterCloseRefused(t *testing.T) {
	var dials int32
	m := testManager(reloadCfg(stdio("alpha")), &dials, 0)
	before := m.allEntries()
	if err := m.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	res := m.Reload(reloadCfg(stdio("beta")))
	if res.Err == "" || len(res.Applied) != 0 || len(res.RestartNeeded) != 0 {
		t.Fatalf("отказ после Close: %+v", res)
	}
	if m.find("beta") != nil {
		t.Fatal("сервер из отвергнутой перезагрузки попал в набор")
	}
	after := m.allEntries()
	if len(after) != len(before) || after[0] != before[0] {
		t.Fatalf("записи подменены после Close: было %d, стало %d", len(before), len(after))
	}
}

// Откат транспорта снимает и таблицу профилей: отображение ключ→профиль
// осталось прежним, поэтому Applied не должен обещать применённые profiles.
func TestReloadRevertsProfilesWithTransport(t *testing.T) {
	var dials int32
	oldProfiles := map[string]*config.Profile{"reader": {Deny: []string{"danger-zone"}}}
	m := testManager(&config.Config{
		Servers: []*config.Server{stdio("alpha")},
		Transport: &config.HTTPTransport{
			Type: "http", AuthToken: "old-token", Host: "127.0.0.1",
			KeyProfiles: map[string]string{"fp1": "reader"},
		},
		Profiles: oldProfiles,
	}, &dials, 0)
	defer m.Close()

	res := m.Reload(&config.Config{
		Servers:   []*config.Server{stdio("alpha")},
		Transport: &config.HTTPTransport{Type: "http", AuthToken: "new-token", Host: "0.0.0.0"},
		Profiles:  map[string]*config.Profile{"reader": {}},
	})
	if len(res.RestartNeeded) == 0 {
		t.Fatal("restartNeeded пуст: смена адреса без перезапуска не применяется")
	}
	for _, name := range res.Applied {
		if name == "profiles" {
			t.Fatalf("applied = %v: profiles применены при откате транспорта", res.Applied)
		}
	}
	cfg := m.config()
	if !reflect.DeepEqual(cfg.Profiles, oldProfiles) {
		t.Fatalf("таблица профилей применена при откате транспорта: %+v", cfg.Profiles)
	}
	if cfg.Transport.KeyProfiles["fp1"] != "reader" {
		t.Fatalf("отображение ключ→профиль потеряно: %v", cfg.Transport.KeyProfiles)
	}
}
