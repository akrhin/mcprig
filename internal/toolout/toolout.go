// Package toolout keeps the tool-out directory, where truncated answers are
// saved, from growing without bound: age limit, size cap, and a periodic sweep.
package toolout

import (
	"context"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	// DefaultTTL is how long a saved answer stays readable.
	DefaultTTL = 24 * time.Hour
	// DefaultMax is the size cap of one tool-out directory.
	DefaultMax = int64(256 << 20)
	// Interval is how often a running process sweeps.
	Interval = time.Hour
	// minAge protects a file a client may still be about to read: eviction by
	// size never touches it. A TTL shorter than this is honoured as asked.
	minAge = 5 * time.Minute
)

// Config is the retention policy for tool-out.
type Config struct {
	TTL time.Duration
	Max int64
}

// Base is the data directory this process writes its tool-out into.
func Base() string {
	if dir := os.Getenv("MCPRIG_DATA_DIR"); dir != "" {
		return dir
	}
	return os.TempDir()
}

// Dir is where an oversized answer is written; created on first use.
func Dir(base string) string { return filepath.Join(base, "tool-out") }

// FromEnv reads MCPRIG_TOOLOUT_TTL and MCPRIG_TOOLOUT_MAX. A value that does
// not parse is reported back as a warning and the default is used.
func FromEnv() (Config, []string) {
	cfg := Config{TTL: DefaultTTL, Max: DefaultMax}
	var warns []string
	if v := strings.TrimSpace(os.Getenv("MCPRIG_TOOLOUT_TTL")); v != "" {
		switch d, err := time.ParseDuration(v); {
		case err != nil:
			warns = append(warns, fmt.Sprintf("MCPRIG_TOOLOUT_TTL=%q: %v — беру %s", v, err, DefaultTTL))
		case d <= 0:
			warns = append(warns, fmt.Sprintf("MCPRIG_TOOLOUT_TTL=%q: срок должен быть положительным — беру %s", v, DefaultTTL))
		default:
			cfg.TTL = d
		}
	}
	if v := strings.TrimSpace(os.Getenv("MCPRIG_TOOLOUT_MAX")); v != "" {
		switch n, err := ParseSize(v); {
		case err != nil:
			warns = append(warns, fmt.Sprintf("MCPRIG_TOOLOUT_MAX=%q: %v — беру %d байт", v, err, DefaultMax))
		case n <= 0:
			warns = append(warns, fmt.Sprintf("MCPRIG_TOOLOUT_MAX=%q: объём должен быть положительным — беру %d байт", v, DefaultMax))
		default:
			cfg.Max = n
		}
	}
	return cfg, warns
}

var sizeUnits = map[string]int64{
	"": 1, "b": 1,
	"k": 1 << 10, "kb": 1 << 10, "kib": 1 << 10,
	"m": 1 << 20, "mb": 1 << 20, "mib": 1 << 20,
	"g": 1 << 30, "gb": 1 << 30, "gib": 1 << 30,
}

// ParseSize accepts plain bytes and the K/KiB/M/MiB/G/GiB suffixes.
func ParseSize(s string) (int64, error) {
	lower := strings.ToLower(strings.TrimSpace(s))
	num, unit := lower, ""
	for i := 0; i < len(lower); i++ {
		if lower[i] < '0' || lower[i] > '9' {
			num, unit = lower[:i], lower[i:]
			break
		}
	}
	mult, ok := sizeUnits[unit]
	if !ok {
		return 0, fmt.Errorf("непонятная единица %q", unit)
	}
	n, err := strconv.ParseInt(num, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("не число: %q", strings.TrimSpace(s))
	}
	if n > (1<<62)/mult {
		return 0, fmt.Errorf("слишком большое значение: %q", strings.TrimSpace(s))
	}
	return n * mult, nil
}

// Report is what one sweep did.
type Report struct {
	Removed int
	Freed   int64
}

// Sweep applies the policy to base/tool-out and every set's tool-out under
// base: the panel shares one data directory with its set proxies.
func Sweep(base string, cfg Config, now time.Time) (Report, error) {
	var rep Report
	var firstErr error
	for _, dir := range dirs(base) {
		r, err := sweepDir(dir, cfg, now)
		rep.Removed += r.Removed
		rep.Freed += r.Freed
		if err != nil && !os.IsNotExist(err) && firstErr == nil {
			firstErr = err
		}
	}
	return rep, firstErr
}

// dirs lists the tool-out directories a process may own: its own and those of
// the sets it supervises.
func dirs(base string) []string {
	out := []string{Dir(base)}
	sets, _ := filepath.Glob(filepath.Join(base, "sets", "*", "tool-out"))
	return append(out, sets...)
}

type entry struct {
	path string
	size int64
	mod  time.Time
}

func sweepDir(dir string, cfg Config, now time.Time) (Report, error) {
	list, err := os.ReadDir(dir)
	if err != nil {
		return Report{}, err
	}
	var rep Report
	var total int64
	fresh := make([]entry, 0, len(list))
	for _, de := range list {
		if de.IsDir() {
			continue
		}
		info, err := de.Info()
		if err != nil {
			continue // файл исчез между чтением каталога и Stat
		}
		e := entry{filepath.Join(dir, de.Name()), info.Size(), info.ModTime()}
		if now.Sub(e.mod) > cfg.TTL && os.Remove(e.path) == nil {
			rep.Removed++
			rep.Freed += e.size
			continue
		}
		fresh = append(fresh, e)
		total += e.size
	}
	if total <= cfg.Max {
		return rep, nil
	}
	// Превышен объём: самые старые первыми, но не моложе minAge — свежий файл
	// может ещё читаться по пути из маркера.
	sort.Slice(fresh, func(i, j int) bool { return fresh[i].mod.Before(fresh[j].mod) })
	for _, e := range fresh {
		if total <= cfg.Max {
			break
		}
		if now.Sub(e.mod) < minAge {
			continue
		}
		if os.Remove(e.path) == nil {
			rep.Removed++
			rep.Freed += e.size
			total -= e.size
		}
	}
	return rep, nil
}

// Run sweeps now and then every interval until ctx is done. The returned
// channel closes when the goroutine exits, so a test can wait for it.
func Run(ctx context.Context, base string, cfg Config, interval time.Duration, log *slog.Logger) <-chan struct{} {
	if interval <= 0 {
		interval = Interval
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		t := time.NewTicker(interval)
		defer t.Stop()
		for {
			sweepAndLog(base, cfg, log)
			select {
			case <-ctx.Done():
				return
			case <-t.C:
			}
		}
	}()
	return done
}

func sweepAndLog(base string, cfg Config, log *slog.Logger) {
	rep, err := Sweep(base, cfg, time.Now())
	if err != nil && log != nil {
		log.Warn("tool-out: чистка", "err", err)
	}
	if rep.Removed > 0 && log != nil {
		log.Info("tool-out: удалено", "файлов", rep.Removed, "байт", rep.Freed)
	}
}

// Usage sums the tool-out files under base and reports the oldest one — the
// next to expire. fs.ErrNotExist means there is no tool-out at all.
func Usage(base string) (size int64, count int, oldest time.Time, err error) {
	found := false
	for _, dir := range dirs(base) {
		list, err := os.ReadDir(dir)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return 0, 0, time.Time{}, err
		}
		found = true
		for _, de := range list {
			if de.IsDir() {
				continue
			}
			info, err := de.Info()
			if err != nil {
				continue
			}
			count++
			size += info.Size()
			if oldest.IsZero() || info.ModTime().Before(oldest) {
				oldest = info.ModTime()
			}
		}
	}
	if !found {
		return 0, 0, time.Time{}, fs.ErrNotExist
	}
	return size, count, oldest, nil
}
