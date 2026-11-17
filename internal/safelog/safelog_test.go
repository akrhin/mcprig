package safelog

import "testing"

func TestValue(t *testing.T) {
	if got := Value("set\r\nчужой лог\tещё"); got != "setчужой логещё" {
		t.Fatalf("Value вырезал не только управляющие: %q", got)
	}
	if got := Value("набор-1"); got != "набор-1" {
		t.Fatalf("чистое значение изменено: %q", got)
	}
}
