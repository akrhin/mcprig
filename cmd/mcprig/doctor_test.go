package main

import (
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/akrhin/mcprig/internal/registry"
)

// doctorFreePort берёт свободный порт из диапазона наборов: слушатель
// открывается и сразу закрывается, порт остаётся закрытым, но занятым на
// момент проверки не считается.
func doctorFreePort(t *testing.T) int {
	t.Helper()
	for port := registry.LastPort; port >= registry.FirstPort; port-- {
		ln, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
		if err == nil {
			ln.Close()
			return port
		}
	}
	t.Fatal("нет свободного порта в диапазоне наборов")
	return 0
}

func writeDoctorFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("запись %s: %v", path, err)
	}
}

// doctor читает panel.env, а не legacy env: юнит держит пароль там, и
// значение в кавычках сохраняет пробелы по краям.
func TestLoadPanelEnvPanelFirst(t *testing.T) {
	dir := t.TempDir()
	const key, legacy = "MCPRIG_DOCTOR_TEST_QUOTED", "MCPRIG_DOCTOR_TEST_LEGACY"
	os.Unsetenv(key)
	os.Unsetenv(legacy)
	t.Cleanup(func() {
		os.Unsetenv(key)
		os.Unsetenv(legacy)
	})
	writeDoctorFile(t, filepath.Join(dir, "panel.env"),
		"# секреты панели\n"+key+"=\"p a s s\"\n")
	writeDoctorFile(t, filepath.Join(dir, "env"),
		key+"=from-legacy\n"+legacy+"='v'\n")

	line, ok := loadPanelEnv(dir)
	if !ok {
		t.Fatal("loadPanelEnv: файлов не найдено")
	}
	if !strings.Contains(line.Text, "panel.env") {
		t.Errorf("в строке нет panel.env: %q", line.Text)
	}
	if got := os.Getenv(key); got != "p a s s" {
		t.Errorf("%s: want %q, got %q", key, "p a s s", got)
	}
	if got := os.Getenv(legacy); got != "v" {
		t.Errorf("%s: want %q, got %q", legacy, "v", got)
	}
}

// Таблица синтаксиса, который parseEnvFile обещает: export, пробелы вокруг
// имени, пустое значение, кавычки и незакрытая кавычка.
func TestParseEnvFile(t *testing.T) {
	body := strings.Join([]string{
		"# комментарий",
		"export K=v",
		"Kspaced = v2",
		"EMPTY=",
		`DQ="a\"b\\c"`,
		`UNCLOSED="x`,
		`SQ='p a s s'`,
	}, "\n")
	want := map[string]string{
		"K":        "v",
		"Kspaced":  "v2",
		"EMPTY":    "",
		"DQ":       `a"b\c`,
		"UNCLOSED": `"x`,
		"SQ":       "p a s s",
	}
	got := parseEnvFile(body)
	if len(got) != len(want) {
		t.Fatalf("переменных: want %d, got %d (%v)", len(want), len(got), got)
	}
	for key, value := range want {
		if got[key] != value {
			t.Errorf("%s: want %q, got %q", key, value, got[key])
		}
	}
}

// doctor зарегистрирован как подкоманда и разбирает свои флаги.
func TestParseArgsDoctor(t *testing.T) {
	mode, rest, err := parseArgs([]string{"doctor", "-json"})
	if err != nil || mode != "doctor" || len(rest) != 1 || rest[0] != "-json" {
		t.Fatalf("doctor: mode=%q rest=%q err=%v", mode, rest, err)
	}
	if _, _, err := parseArgs([]string{"doctor", "лишнее"}); err == nil {
		t.Fatal("doctor: лишний аргумент должен быть ошибкой")
	}
}

// Отсутствующая ${VAR} — проблема, найденная — ok, и значение нигде не
// печатается.
func TestCheckSecretsMissingAndPresent(t *testing.T) {
	for _, name := range []string{"BAO_ADDR", "BAO_TOKEN", "OPENBAO_ADDR", "OPENBAO_TOKEN"} {
		t.Setenv(name, "")
	}
	// t.Setenv не умеет снимать переменную: убираем вручную и возвращаем.
	old, had := os.LookupEnv("DOCTOR_ABSENT_VAR")
	os.Unsetenv("DOCTOR_ABSENT_VAR")
	t.Cleanup(func() {
		if had {
			os.Setenv("DOCTOR_ABSENT_VAR", old)
		}
	})
	dir := t.TempDir()

	raw := []byte(`{"servers":[{"name":"a","command":["x"],"env":{"TOKEN":"${DOCTOR_ABSENT_VAR}"}}]}`)
	lines := checkSecrets(raw, dir)
	if len(lines) != 1 || lines[0].OK || !strings.Contains(lines[0].Text, "DOCTOR_ABSENT_VAR") {
		t.Fatalf("отсутствующая переменная: %+v", lines)
	}

	t.Setenv("DOCTOR_PRESENT_VAR", "значение-которое-нельзя-печатать")
	raw = []byte(`{"servers":[{"name":"a","command":["x"],"env":{"TOKEN":"${DOCTOR_PRESENT_VAR}"}}]}`)
	lines = checkSecrets(raw, dir)
	if len(lines) != 1 || !lines[0].OK || !strings.Contains(lines[0].Text, "окружение") {
		t.Fatalf("найденная переменная: %+v", lines)
	}
	if strings.Contains(lines[0].Text, "значение-которое-нельзя-печатать") {
		t.Fatalf("значение секрета попало в вывод: %q", lines[0].Text)
	}
}

// Набор с autostart на закрытом порту — проблема.
func TestCheckSetsDeadPort(t *testing.T) {
	port := doctorFreePort(t)
	reg := &registry.Data{Sets: []*registry.Set{{
		ID:        "main",
		Port:      port,
		Autostart: true,
		Keys:      []registry.Key{{ID: "k1", Token: "0123456789abcdef", CreatedAt: "2026-11-09T00:00:00Z"}},
	}}}
	sets, keys := checkSets(reg, nil, t.TempDir(), "0.0.0.0")
	if len(sets) != 1 || sets[0].OK {
		t.Fatalf("мёртвый порт: %+v", sets)
	}
	if !strings.Contains(sets[0].Text, "не слушает") {
		t.Fatalf("мёртвый порт: %q", sets[0].Text)
	}
	if len(keys) != 1 || !keys[0].OK {
		t.Fatalf("ключи: %+v", keys)
	}
}

// Зелёный случай целиком: конфиг с одним сервером, реестр с одним остановленным
// набором и активным ключом, отвечающая панель, пустой диапазон чужих портов.
func TestBuildDoctorReportGreen(t *testing.T) {
	dataDir := t.TempDir()
	port := doctorFreePort(t)

	registryBody := `{"version":1,"nextPort":` + strconv.Itoa(port) + `,` +
		`"servers":[{"name":"a","command":["x"],"enabled":true}],` +
		`"sets":[{"id":"main","name":"main","servers":["a"],"port":` + strconv.Itoa(port) + `,` +
		`"keys":[{"id":"k1","token":"0123456789abcdef","createdAt":"2026-11-09T00:00:00Z"}]}]}`
	writeDoctorFile(t, filepath.Join(dataDir, registry.RegistryFile), registryBody)

	configPath := filepath.Join(t.TempDir(), "servers.json")
	writeDoctorFile(t, configPath, `{"servers":[{"name":"a","command":["x"],"enabled":true,"env":{"K":"v"}}]}`)

	panel := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/healthz" {
			w.WriteHeader(http.StatusOK)
			if _, err := io.WriteString(w, `{"status":"ok","sets":1}`); err != nil {
				t.Errorf("ответ панели: %v", err)
			}
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer panel.Close()
	host, portText, err := net.SplitHostPort(strings.TrimPrefix(panel.URL, "http://"))
	if err != nil {
		t.Fatalf("URL панели: %v", err)
	}
	panelPort, _ := strconv.Atoi(portText)

	rep := buildDoctorReport(doctorOptions{
		ConfigPath: configPath,
		DataDir:    dataDir,
		BindIP:     "0.0.0.0",
		PanelHost:  host,
		PanelPort:  panelPort,
		FirstPort:  port,
		LastPort:   port,
		Version:    "test",
		Executable: "/bin/mcprig",
	}, doctorLine{}, false)
	if !rep.OK || rep.Problems != 0 {
		t.Fatalf("ожидался зелёный отчёт, проблем %d:\n%s", rep.Problems, renderDoctor(rep))
	}
}

// Предупреждение читается как «внимание:», а не как «ok:»; проблема важнее.
func TestRenderDoctorPrefixes(t *testing.T) {
	rep := doctorReport{
		Version: []doctorLine{{OK: true, Text: "0.3.8"}},
		Services: []doctorLine{
			{OK: true, Text: "предупреждение: песочница: prlimit не найден в PATH"},
			{OK: true, Text: "панель слушает 9091"},
			{OK: false, Text: "набор ops не отвечает"},
		},
	}
	out := string(renderDoctor(rep))
	for _, want := range []string{
		"внимание: песочница: prlimit не найден в PATH",
		"ok: панель слушает 9091",
		"проблема: набор ops не отвечает",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("нет строки %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "ok: предупреждение:") {
		t.Fatalf("предупреждение выдано за успех:\n%s", out)
	}
}

// Пустая установка — не поломка: одна строка про init, ноль проблем.
// Панель при этом не щупается: недостижимый порт панели в опциях не должен
// превращаться в проблему на установке, которой ещё нет.
func TestDoctorEmptyInstall(t *testing.T) {
	rep := buildDoctorReport(doctorOptions{
		ConfigPath: filepath.Join(t.TempDir(), "servers.json"),
		DataDir:    t.TempDir(),
		BindIP:     "0.0.0.0",
		PanelHost:  "127.0.0.1",
		PanelPort:  1,
		FirstPort:  registry.FirstPort,
		LastPort:   registry.LastPort,
		Version:    "test",
		Executable: "/bin/mcprig",
	}, doctorLine{}, false)
	if !rep.OK || rep.Problems != 0 {
		t.Fatalf("пустая установка: проблем %d\n%s", rep.Problems, renderDoctor(rep))
	}
	out := string(renderDoctor(rep))
	if !strings.Contains(out, "mcprig init") || !strings.Contains(out, "итог: 0 проблем") {
		t.Fatalf("нет строки про init:\n%s", out)
	}
	if strings.Contains(out, "панель") {
		t.Fatalf("пустая установка трогала панель:\n%s", out)
	}

	body, err := json.MarshalIndent(rep, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{`"sets": []`, `"keys": []`, `"secrets": []`, `"services": []`} {
		if !strings.Contains(string(body), key) {
			t.Fatalf("в JSON нет %s:\n%s", key, body)
		}
	}
}

// Битый реестр называет путь один раз: readRegistry уже обернул ошибку.
// Строка повторяется в двух разделах (наборы и ключи) — итого два раза.
func TestDoctorBrokenRegistryNamesPathOnce(t *testing.T) {
	dataDir := t.TempDir()
	writeDoctorFile(t, filepath.Join(dataDir, registry.RegistryFile), `{"sets": [`)
	rep := buildDoctorReport(doctorOptions{
		ConfigPath: filepath.Join(t.TempDir(), "servers.json"),
		DataDir:    dataDir,
		BindIP:     "0.0.0.0",
		PanelHost:  "127.0.0.1",
		PanelPort:  1,
		FirstPort:  registry.FirstPort,
		LastPort:   registry.LastPort,
		Version:    "test",
		Executable: "/bin/mcprig",
	}, doctorLine{}, false)
	if rep.OK || rep.Problems == 0 {
		t.Fatalf("битый реестр — не проблема:\n%s", renderDoctor(rep))
	}
	out := string(renderDoctor(rep))
	named := "реестр " + filepath.Join(dataDir, registry.RegistryFile)
	if got := strings.Count(out, named); got != 2 {
		t.Fatalf("путь назван %d раз, ожидалось 2:\n%s", got, out)
	}
}

// Пустой каталог даёт exit 0, битый реестр — прежний код 1.
func TestDoctorExitCode(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "servers.json")
	t.Setenv("MCPRIG_DATA_DIR", t.TempDir())
	if err := runDoctor([]string{"-config", configPath}); err != nil {
		t.Fatalf("пустая установка: %v", err)
	}

	dir := t.TempDir()
	writeDoctorFile(t, filepath.Join(dir, registry.RegistryFile), `{"sets": [`)
	t.Setenv("MCPRIG_DATA_DIR", dir)
	if err := runDoctor([]string{"-config", configPath}); !errors.Is(err, errDoctorProblems) {
		t.Fatalf("битый реестр: want errDoctorProblems, got %v", err)
	}
}

// Конфиг есть, реестра нет — прежнее поведение: проблема, а не «пусто».
func TestDoctorConfigWithoutRegistry(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "servers.json")
	writeDoctorFile(t, configPath, `{"servers":[{"name":"a","command":["x"],"enabled":true}]}`)
	dataDir := t.TempDir()
	rep := buildDoctorReport(doctorOptions{
		ConfigPath: configPath,
		DataDir:    dataDir,
		BindIP:     "0.0.0.0",
		PanelHost:  "127.0.0.1",
		PanelPort:  1,
		FirstPort:  registry.FirstPort,
		LastPort:   registry.LastPort,
		Version:    "test",
		Executable: "/bin/mcprig",
	}, doctorLine{}, false)
	if rep.OK || rep.Problems == 0 {
		t.Fatalf("ожидалась проблема:\n%s", renderDoctor(rep))
	}
	if out := string(renderDoctor(rep)); !strings.Contains(out, filepath.Join(dataDir, registry.RegistryFile)) {
		t.Fatalf("нет упоминания реестра:\n%s", out)
	}
}

// Строка tool-out: число файлов, объём и возраст самого старого — он же
// ближайший к истечению срока.
func TestToolOutLine(t *testing.T) {
	dataDir := t.TempDir()
	dir := filepath.Join(dataDir, "tool-out")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"a.txt", "b.txt"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	old := time.Now().Add(-6 * time.Hour)
	if err := os.Chtimes(filepath.Join(dir, "a.txt"), old, old); err != nil {
		t.Fatal(err)
	}

	line := toolOutLine(dataDir)
	if !line.OK || !strings.Contains(line.Text, "2 файла") || !strings.Contains(line.Text, "самый старый") || !strings.Contains(line.Text, "ч назад") {
		t.Fatalf("строка: %+v", line)
	}
	if got := toolOutLine(t.TempDir()); got.Text != "tool-out: файлов нет" {
		t.Fatalf("пустой каталог: %q", got.Text)
	}
}

// TestCheckRedirect: строка показывает действующий redirect и отбивает адрес,
// по которому провайдер не сможет прислать код.
func TestCheckRedirect(t *testing.T) {
	cases := []struct {
		publicURL string
		host      string
		port      int
		wantOK    bool
		want      string
	}{
		{"https://panel.example/mcprig", "127.0.0.1", 9091, true, "redirect OAuth: https://panel.example/mcprig/oauth/callback"},
		{"", "127.0.0.1", 9091, true, "redirect OAuth: http://127.0.0.1:9091/oauth/callback"},
		{"", "127.0.0.1", 0, false, "порт 0"},
		{"https://127.0.0.1", "127.0.0.1", 9091, false, "loopback"},
	}
	for _, c := range cases {
		line := checkRedirect(doctorOptions{PublicURL: c.publicURL, PanelHost: c.host, PanelPort: c.port})
		if line.OK != c.wantOK || !strings.Contains(line.Text, c.want) {
			t.Fatalf("publicUrl=%q host=%q port=%d: получено %+v", c.publicURL, c.host, c.port, line)
		}
	}
}

// TestCheckRedirectBadPublicURL: битый publicUrl — проблема в doctor, а не
// зелёная строка с адресом, который провайдер отвергнет.
func TestCheckRedirectBadPublicURL(t *testing.T) {
	for _, publicURL := range []string{"https://host#", "mcprig.example", "https://host/prefix/"} {
		line := checkRedirect(doctorOptions{PublicURL: publicURL, PanelHost: "127.0.0.1", PanelPort: 9091})
		if line.OK || !strings.Contains(line.Text, "publicUrl") {
			t.Fatalf("publicUrl=%q: получено %+v", publicURL, line)
		}
	}
	if line := checkRedirect(doctorOptions{PanelHost: "", PanelPort: 9091}); line.Text == "" {
		t.Fatalf("пустой host дал пустую строку")
	}
}

// Открытая панель — не проблема (exit-код не меняется), но и не «ok»:
// строка начинается с «предупреждение:» и печатается как «внимание:».
func TestDoctorWarnsPanelWithoutPassword(t *testing.T) {
	dataDir := t.TempDir()
	port := doctorFreePort(t)
	registryBody := `{"version":1,"nextPort":` + strconv.Itoa(port) + `,"servers":[],"sets":[]}`
	writeDoctorFile(t, filepath.Join(dataDir, registry.RegistryFile), registryBody)
	configPath := filepath.Join(t.TempDir(), "servers.json")
	writeDoctorFile(t, configPath, `{"servers":[]}`)

	panel := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		if _, err := io.WriteString(w, `{"status":"ok","sets":0}`); err != nil {
			t.Errorf("ответ панели: %v", err)
		}
	}))
	defer panel.Close()
	host, portText, err := net.SplitHostPort(strings.TrimPrefix(panel.URL, "http://"))
	if err != nil {
		t.Fatalf("URL панели: %v", err)
	}
	panelPort, _ := strconv.Atoi(portText)

	opts := doctorOptions{
		ConfigPath: configPath,
		DataDir:    dataDir,
		BindIP:     "0.0.0.0",
		PanelHost:  host,
		PanelPort:  panelPort,
		FirstPort:  port,
		LastPort:   port,
		Version:    "test",
		Executable: "/bin/mcprig",
		AuthOff:    true,
	}
	rep := buildDoctorReport(opts, doctorLine{}, false)
	if !rep.OK || rep.Problems != 0 {
		t.Fatalf("предупреждение не должно быть проблемой:\n%s", renderDoctor(rep))
	}
	out := string(renderDoctor(rep))
	if !strings.Contains(out, "внимание: панель без пароля") {
		t.Fatalf("нет предупреждения об открытой панели:\n%s", out)
	}

	// Пароль задан — предупреждения нет.
	opts.HasPassword = true
	if out := string(renderDoctor(buildDoctorReport(opts, doctorLine{}, false))); strings.Contains(out, "без пароля") {
		t.Fatalf("предупреждение при заданном пароле:\n%s", out)
	}
}
