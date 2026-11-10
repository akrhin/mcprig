package manager

import "testing"

// Логирование в просмотре набора показывается по факту: инструмент
// logging/setLevel в списке, а не deprecated-capability в handshake.
func TestBrowseLoggingFromToolList(t *testing.T) {
	tools := []browseTool{{Name: "echo"}, {Name: loggingToolName}}
	if !offersLogging(tools) {
		t.Fatal("logging/setLevel в списке, а logging не показан")
	}
	if offersLogging([]browseTool{{Name: "echo"}, {Name: "fetch"}}) {
		t.Fatal("logging показан без инструмента")
	}
	if offersLogging(nil) {
		t.Fatal("пустой список дал logging")
	}
}
