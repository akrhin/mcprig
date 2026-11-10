package main

import (
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
			io.WriteString(w, `{"status":"ok","sets":1}`)
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
	if !line.OK || !strings.Contains(line.Text, "2 файлов") || !strings.Contains(line.Text, "самый старый") || !strings.Contains(line.Text, "ч назад") {
		t.Fatalf("строка: %+v", line)
	}
	if got := toolOutLine(t.TempDir()); got.Text != "tool-out нет" {
		t.Fatalf("пустой каталог: %q", got.Text)
	}
}
