package downstream

import (
	"reflect"
	"testing"
)

// Замена бинарника переименованием ломала запуск серверов: /proc/self/exe
// кончается на « (deleted)». Сторож обязан уступать место самому серверу.
func TestGuardArgvFallsBackWithoutExecutable(t *testing.T) {
	server := []string{"npx", "-y", "pkg"}
	if got := guardArgv(server, ""); !reflect.DeepEqual(got, server) {
		t.Fatalf("без сторожа ждали %v, получили %v", server, got)
	}
	got := guardArgv(server, "/usr/bin/mcprig")
	want := []string{"/usr/bin/mcprig", "child-guard", "--", "npx", "-y", "pkg"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("со сторожем ждали %v, получили %v", want, got)
	}
	if !fileExists("/proc/self/exe") || fileExists("/proc/self/exe-deleted") {
		t.Fatal("fileExists врёт про существование пути")
	}
}
