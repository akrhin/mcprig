package main

import "testing"

// TestWithScheme: адрес без схемы — то, что человек копирует из `ss` или из
// браузера, — раньше падал на разборе URL до первого запроса.
func TestWithScheme(t *testing.T) {
	cases := map[string]string{
		"127.0.0.1:9091":        "http://127.0.0.1:9091",
		"http://panel:9088":     "http://panel:9088",
		"https://panel.example": "https://panel.example",
	}
	for in, want := range cases {
		if got := withScheme(in); got != want {
			t.Fatalf("withScheme(%q) = %q, ждали %q", in, got, want)
		}
	}
}
