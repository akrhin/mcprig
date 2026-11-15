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
