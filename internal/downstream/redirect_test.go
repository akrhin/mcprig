package downstream

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// Заголовки сервера (в том числе Authorization) ставятся на каждый RoundTrip,
// поэтому редирект на другой хост унёс бы их чужому серверу.
func TestSameHostRedirect(t *testing.T) {
	start, _ := url.Parse("https://mcp.example/sse")
	via := []*http.Request{{URL: start}}

	same, _ := url.Parse("https://mcp.example/sse2")
	if err := sameHostRedirect(&http.Request{URL: same}, via); err != nil {
		t.Fatalf("редирект на тот же хост отклонён: %v", err)
	}

	other, _ := url.Parse("https://attacker.example/collect")
	err := sameHostRedirect(&http.Request{URL: other}, via)
	if err == nil || !strings.Contains(err.Error(), "другой хост") {
		t.Fatalf("кросс-хостовый редирект пропущен: %v", err)
	}
}
