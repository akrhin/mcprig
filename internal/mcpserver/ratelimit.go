package mcpserver

import (
	"crypto/sha256"
	"encoding/hex"
	"sync"
	"time"

	"golang.org/x/time/rate"

	"github.com/akrhin/mcprig/internal/config"
)

const (
	// bucketSweepEvery — как часто (в вызовах) убирать простаивающие корзины:
	// ключ включает пару (клиент, инструмент), и без уборки map растёт от
	// каждого выдуманного имени инструмента.
	bucketSweepEvery = 1024
	// bucketIdle — сколько корзина живёт без вызова.
	bucketIdle = 10 * time.Minute
	// maxBuckets — потолок карты: ключ строит клиент (имя инструмента в вызове),
	// и один поток уникальных имён иначе держал бы память до bucketIdle.
	// Вытеснение идёт внутри идентичности, которая и вызвала переполнение, —
	// так чужой поток имён не отбирает квоту у соседа. shortcut: своя же
	// переполнившая идентичность может потерять свою корзину и получить burst
	// заново; лимит — тормоз, не граница безопасности.
	maxBuckets = 4096
)

// bucket — token bucket одной пары (клиент, server/tool) и метка последнего
// использования для уборки. ident лежит рядом, чтобы вытеснять корзины того,
// кто вызвал переполнение.
type bucket struct {
	ident string
	lim   *rate.Limiter
	used  time.Time
}

// bucketKey hashes the identity with the pair: the tool name comes from the
// caller, so a raw string would park a megabyte-long key per invented name.
func bucketKey(ident, server, tool string) string {
	sum := sha256.Sum256([]byte(ident + "\x00" + server + "/" + tool))
	return hex.EncodeToString(sum[:8])
}

// rateLimits держит корзины набора. nil-значение (блока нет или он нулевой) —
// лимита нет, и никакого состояния не заводится.
type rateLimits struct {
	cfg     *config.RateLimit
	mu      sync.Mutex
	buckets map[string]*bucket
	calls   int
}

func newRateLimits(cfg *config.RateLimit) *rateLimits {
	if cfg.Empty() {
		return nil
	}
	return &rateLimits{cfg: cfg, buckets: make(map[string]*bucket, 4)}
}

// limitsFor picks a bucket's limit: perTool overrides the set-wide value, and a
// zero value on either level means that pair is unlimited.
func (l *rateLimits) limitsFor(server, tool string) (perMinute, burst int) {
	if t, ok := l.cfg.PerTool[server+"/"+tool]; ok {
		return t.PerMinute, t.Burst
	}
	return l.cfg.PerMinute, l.cfg.Burst
}

// allow takes one token for (ident, server/tool). A refused call comes back
// with the delay the same token bucket expects the caller to wait.
func (l *rateLimits) allow(ident, server, tool string) (ok bool, perMinute int, retry time.Duration) {
	perMinute, burst := l.limitsFor(server, tool)
	if perMinute <= 0 {
		return true, 0, 0
	}
	if burst <= 0 {
		burst = perMinute
	}
	key := bucketKey(ident, server, tool)
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	b := l.buckets[key]
	if b == nil {
		if len(l.buckets) >= maxBuckets {
			l.sweep(now)
			if len(l.buckets) >= maxBuckets {
				l.evict(ident)
			}
		}
		b = &bucket{ident: ident, lim: rate.NewLimiter(rate.Limit(float64(perMinute)/60), burst)}
		l.buckets[key] = b
	}
	b.used = now
	res := b.lim.Reserve()
	delay := res.Delay()
	if delay > 0 {
		// Отвергнутый вызов не тратит токен: иначе отказ отнимал бы право
		// на следующий вызов у того, кому этот отказ выдан.
		res.Cancel()
	}
	l.calls++
	if l.calls%bucketSweepEvery == 0 {
		l.sweep(now)
	}
	return delay <= 0, perMinute, delay
}

// evict drops the least recently used bucket of ident when the cap is reached
// after a sweep — the caller that overflowed pays, not its neighbours; only when
// that identity holds nothing does it fall back to the oldest bucket overall.
// The walk is linear, but it only happens on a flood of distinct names; the
// ordinary path stays one map lookup.
func (l *rateLimits) evict(ident string) {
	var ownKey, anyKey string
	var ownUsed, anyUsed time.Time
	for key, b := range l.buckets {
		if anyKey == "" || b.used.Before(anyUsed) {
			anyKey, anyUsed = key, b.used
		}
		if b.ident == ident && (ownKey == "" || b.used.Before(ownUsed)) {
			ownKey, ownUsed = key, b.used
		}
	}
	if ownKey == "" {
		ownKey = anyKey
	}
	delete(l.buckets, ownKey)
}

// sweep drops buckets idle for bucketIdle. It runs inline under l.mu every
// bucketSweepEvery calls: no goroutine to stop with the Manager.
func (l *rateLimits) sweep(now time.Time) {
	for key, b := range l.buckets {
		if now.Sub(b.used) > bucketIdle {
			delete(l.buckets, key)
		}
	}
}
