package safepath

import (
	"strings"
	"testing"
)

func TestName(t *testing.T) {
	for _, s := range []string{"a", "set1", "my-set", "a_b", "0", strings.Repeat("a", 64)} {
		if !Name(s) {
			t.Fatalf("Name(%q) = false, ожидалось true", s)
		}
	}
	for _, s := range []string{"", "..", ".", "a/b", "../x", "/etc", "Set", "set.", "-x", "a b", "a\x00b", strings.Repeat("a", 65)} {
		if Name(s) {
			t.Fatalf("Name(%q) = true, ожидалось false", s)
		}
	}
}

func TestPath(t *testing.T) {
	for _, p := range []string{"/", "/a/b", "/a/b/raw-2026-11-17.jsonl"} {
		if !Path(p) {
			t.Fatalf("Path(%q) = false, ожидалось true", p)
		}
	}
	for _, p := range []string{"", "a/b", "../etc", "/a/../b", "/a\x00b", "a/../b"} {
		if Path(p) {
			t.Fatalf("Path(%q) = true, ожидалось false", p)
		}
	}
}
