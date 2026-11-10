package manager

import (
	"strings"
	"testing"
	"time"

	"github.com/akrhin/mcprig/internal/config"
	"github.com/akrhin/mcprig/internal/registry"
)

const baoLag = 300 * time.Millisecond

func laggedFake(names ...string) *fakeBao {
	f := &fakeBao{values: map[string]string{}}
	for _, name := range names {
		f.values[name] = "v-" + name
	}
	f.setDelay(baoLag)
	return f
}

// Восемь переменных по 300 мс: последовательно это 2,4 с, параллельно — около
// одного запроса. Замер обоих обходов в одном прогоне.
func TestResolveParallel(t *testing.T) {
	names := []string{"V1", "V2", "V3", "V4", "V5", "V6", "V7", "V8"}

	seq := fakeSecrets(t, laggedFake(names...), nil)
	start := time.Now()
	for _, name := range names {
		seq.Get(name)
	}
	sequential := time.Since(start)

	par := fakeSecrets(t, laggedFake(names...), nil)
	start = time.Now()
	got := par.Resolve(names)
	parallel := time.Since(start)

	if parallel > 8*baoLag/2 {
		t.Fatalf("8 переменных за %v: обход остался последовательным (последовательный — %v)", parallel, sequential)
	}
	if sequential < 8*baoLag/2 {
		t.Fatalf("последовательный обход занял %v — задержка фейка не сработала, сравнение бессмысленно", sequential)
	}
	t.Logf("последовательно %v, параллельно %v", sequential, parallel)
	for i, name := range names {
		if got[i].value != "v-"+name || got[i].source != sourceOpenBao {
			t.Fatalf("Resolve[%d] = %q из %q, ожидалось %q из openbao", i, got[i].value, got[i].source, "v-"+name)
		}
	}
}

// Порядок: медленная переменная, объявленная первой, не сдвигает чужие
// значения. env собирается по именам, а не по порядку ответов сети.
func TestResolveKeepsDeclaredOrder(t *testing.T) {
	fake := &fakeBao{
		values: map[string]string{"SLOW": "slow-value", "FAST": "fast-value"},
		lags:   map[string]time.Duration{"SLOW": baoLag},
	}
	sec := fakeSecrets(t, fake, nil)

	got := sec.Resolve([]string{"SLOW", "FAST"})
	if got[0].value != "slow-value" || got[1].value != "fast-value" {
		t.Fatalf("Resolve = %q, %q: порядок имён нарушен", got[0].value, got[1].value)
	}

	env := strings.Join(childEnv([]registry.Server{
		&config.Server{Name: "x", Env: map[string]string{"FIRST": "${SLOW}", "SECOND": "${FAST}"}},
	}, sec), "\n")
	for _, want := range []string{"SLOW=slow-value", "FAST=fast-value"} {
		if !strings.Contains(env, want) {
			t.Fatalf("в env нет %q: %s", want, env)
		}
	}
}

// Сбой хранилища: значения пустые, текст ошибки прежний, ошибка не потеряна.
func TestResolveKeepsBaoErrorText(t *testing.T) {
	fake := &fakeBao{values: map[string]string{}, fail: 3}
	sec := fakeSecrets(t, fake, nil)

	got := sec.Resolve([]string{"A", "B", "C"})
	for i, v := range got {
		if v.value != "" || v.source != "" {
			t.Fatalf("Resolve[%d] = %q из %q при сбое OpenBao", i, v.value, v.source)
		}
	}
	if err := sec.RemoteError(); err != "OpenBao: 500 Internal Server Error: boom" {
		t.Fatalf("RemoteError = %q", err)
	}
}
