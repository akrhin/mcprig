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

func TestTextKeepsTab(t *testing.T) {
	if got := Text("a\tb\x1b[31m"); got != "a\tb[31m" {
		t.Fatalf("Text: %q", got)
	}
	if got := Value("a\tb"); got != "ab" {
		t.Fatalf("Value обязан вырезать табуляцию: %q", got)
	}
}
