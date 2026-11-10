package main

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/akrhin/mcprig/internal/toolout"
)

// startToolout — общая для serve и stdio чистка: свип идёт сразу, поэтому
// истёкший файл в базовом каталоге убирается без ожидания тикера.
func TestStartTooloutSweepsImmediately(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("MCPRIG_DATA_DIR", dir)
	// Каталог тот же, куда пишет прокси: stdio не должен заводить свой.
	out := filepath.Join(dir, "tool-out")
	if err := os.MkdirAll(out, 0o700); err != nil {
		t.Fatal(err)
	}
	stale := filepath.Join(out, "stale.txt")
	if err := os.WriteFile(stale, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-48 * time.Hour)
	if err := os.Chtimes(stale, old, old); err != nil {
		t.Fatal(err)
	}

	prev := tooloutInterval
	tooloutInterval = 20 * time.Millisecond
	t.Cleanup(func() { tooloutInterval = prev })

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	startToolout(ctx, slog.New(slog.NewTextHandler(io.Discard, nil)))

	deadline := time.Now().Add(3 * time.Second)
	for {
		if _, err := os.Stat(stale); errors.Is(err, fs.ErrNotExist) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("истёкший файл tool-out не убран")
		}
		time.Sleep(20 * time.Millisecond)
	}
	if _, err := os.Stat(toolout.Dir(toolout.Base())); err != nil {
		t.Fatalf("каталог tool-out: %v", err)
	}
}
