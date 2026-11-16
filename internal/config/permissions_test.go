package config

import (
	"fmt"
	"sync"
	"testing"
)

// Two patterns in one process must keep their own compiled form: a cache keyed
// by the wrong field would answer one rule with the other rule's regexp.
func TestGlobMatchKeepsPatternsApart(t *testing.T) {
	if !globMatch("read_*", "read_x") || globMatch("read_*", "write_x") {
		t.Fatal("read_* ответил не по своему правилу")
	}
	if !globMatch("write_*", "write_x") || globMatch("write_*", "read_x") {
		t.Fatal("write_* ответил не по своему правилу")
	}
	if globMatch("get_?", "get_ab") {
		t.Fatal("get_? не должен совпадать с двумя символами")
	}
}

// The cache is filled under the race detector by the usual parallel callers.
func TestGlobMatchConcurrent(t *testing.T) {
	const workers = 20
	var wg sync.WaitGroup
	errs := make(chan string, workers)
	for i := range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			pattern := fmt.Sprintf("cmd_%d_*", i)
			if !globMatch(pattern, fmt.Sprintf("cmd_%d_x", i)) {
				errs <- pattern + ": не совпал со своим именем"
				return
			}
			if globMatch(pattern, fmt.Sprintf("cmd_%d_x", (i+1)%workers)) {
				errs <- pattern + ": совпал с чужим именем"
			}
		}()
	}
	wg.Wait()
	close(errs)
	for msg := range errs {
		t.Error(msg)
	}
}

// A name that arrives with a line break must not slip past a deny rule: the
// gate sees tool names from upstream, and "." has to match "\n" for "*" to mean
// "any run of characters" in the literal sense.
func TestDenyRuleSpansNewline(t *testing.T) {
	deny := &Permissions{Rules: []Rule{{Pattern: "rm*", Action: PermissionDeny, Message: "rm запрещён"}}}
	if msg, denied := DenyReason(nil, deny, "srv", "rm\n-rf"); !denied {
		t.Fatalf("rm\\n-rf прошёл deny-правило (msg=%q)", msg)
	}
	if _, denied := DenyReason(nil, deny, "srv", "ls"); denied {
		t.Fatal("безвредное имя ls попало под deny")
	}
	if _, denied := DenyReason(nil, deny, "srv", "rm"); !denied {
		t.Fatal("rm должен отбиваться: правило rm* его покрывает")
	}
	// `?` counts one character, and a newline is one.
	single := &Permissions{Rules: []Rule{{Pattern: "a?b", Action: PermissionDeny}}}
	if _, denied := DenyReason(nil, single, "srv", "a\nb"); !denied {
		t.Fatal("a\\nb не отбит правилом a?b: ? не совпал с переводом строки")
	}
	if _, denied := DenyReason(nil, single, "srv", "a\nxb"); denied {
		t.Fatal("a\\nxb отбит правилом a?b, хотя ? — ровно один символ")
	}
}
