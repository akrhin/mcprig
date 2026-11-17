package downstream

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/akrhin/mcprig/internal/config"
)

// Вывод процесса сервера обязан дожить до панели: раньше stderr ребёнка жил
// только в хвосте ошибки подключения, и у успешно поднятого сервера лога не
// было вовсе.
func TestConnectWritesChildStderrToServerLogs(t *testing.T) {
	dir := t.TempDir()
	srv := &config.Server{
		Name:      "noisy",
		Transport: config.TransportStdio,
		Command:   []string{"/bin/sh", "-c", "echo 'лог процесса: строка' >&2; exit 1"},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := (&Connector{Version: "test", ServerLogs: NewServerLogs(dir)}).Connect(ctx, srv); err == nil {
		t.Fatal("want a connect error from a dead server")
	}

	// Копирование stderr идёт отдельной горутиной exec — ждём, а не читаем сразу.
	path := ServerLogPath(dir, "noisy")
	deadline := time.Now().Add(3 * time.Second)
	for {
		body, err := os.ReadFile(path)
		if err == nil && strings.Contains(string(body), "лог процесса: строка") {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("в %s нет вывода процесса: %q, %v", path, body, err)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// Writer один на сервер: переподключение дописывает тот же файл, а не начинает
// его заново, и хвост не растёт выше предела.
func TestServerLogsWriterIsSharedAndBounded(t *testing.T) {
	logs := NewServerLogs(t.TempDir())
	srv := &config.Server{Name: "s1"}
	w := logs.Writer(srv)
	if w == nil {
		t.Fatal("Writer вернул nil для годного имени")
	}
	if again := logs.Writer(srv); again != w {
		t.Fatal("Writer открывает новый файл на каждое подключение")
	}
	if _, err := w.Write([]byte(strings.Repeat("a", serverLogLimit) + "\n")); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte("конец\n")); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(ServerLogPath(logs.setDir, "s1"))
	if err != nil {
		t.Fatal(err)
	}
	if len(body) > serverLogLimit || !strings.HasSuffix(string(body), "конец\n") {
		t.Fatalf("хвост %d байт, конец %q", len(body), string(body[max(0, len(body)-8):]))
	}
	if nw := logs.Writer(&config.Server{Name: "../escape"}); nw != nil {
		t.Fatal("имя вне сегмента пути принято")
	}
}
