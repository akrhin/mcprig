// Contract suite: the same checks run against the TS reference (while it is
// still the behaviour source) and against this Go binary.
//
//	E2E_BASE=http://127.0.0.1:9091 E2E_STRICT=0 go test ./e2e -v   # reference: known defects expected
//	E2E_BASE=http://127.0.0.1:9091 go test ./e2e -v                # product: everything must pass
//	E2E_BASE=... E2E_AUTH=1 go test ./e2e -v                       # also require auth on the API
package e2e

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"
)

var (
	baseURL = strings.TrimRight(os.Getenv("E2E_BASE"), "/")
	strict  = os.Getenv("E2E_STRICT") != "0"
	authOn  = os.Getenv("E2E_AUTH") == "1"
	client  = &http.Client{Timeout: 120 * time.Second}
	tokenRE = regexp.MustCompile(`[0-9a-f]{48}`)
)

type resp struct {
	Status int
	Body   []byte
	JSON   map[string]any
	Any    any
}

func TestMain(m *testing.M) {
	if baseURL == "" {
		// A skipped suite must never read as a passed gate: strict mode (an
		// explicit E2E_STRICT=1) fails instead of skipping.
		fmt.Println("E2E_BASE is empty: contract tests will skip")
		if os.Getenv("E2E_STRICT") == "1" {
			fmt.Println("E2E_STRICT=1: E2E_BASE is required")
			os.Exit(1)
		}
	} else {
		fmt.Printf("contract suite against %s (strict=%v)\n", baseURL, strict)
	}
	os.Exit(m.Run())
}

func call(t *testing.T, method, path string, payload any) resp {
	t.Helper()
	if baseURL == "" {
		t.Skip("E2E_BASE is empty")
	}
	var body io.Reader
	if payload != nil {
		raw, err := json.Marshal(payload)
		if err != nil {
			t.Fatalf("marshal body: %v", err)
		}
		body = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(method, baseURL+path, body)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := client.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	out := resp{Status: res.StatusCode, Body: raw}
	var obj map[string]any
	if json.Unmarshal(raw, &obj) == nil {
		out.JSON = obj
	}
	var anyVal any
	if json.Unmarshal(raw, &anyVal) == nil {
		out.Any = anyVal
	}
	return out
}

// check fails the test unless ok. knownRefDefect marks behaviour the TS
// reference still gets wrong: expected while strict is off, forbidden once
// the suite runs against the product.
func check(t *testing.T, knownRefDefect bool, ok bool, format string, args ...any) {
	t.Helper()
	if ok {
		if knownRefDefect && !strict {
			t.Logf("defect no longer reproduces: "+format, args...)
		}
		return
	}
	if knownRefDefect && !strict {
		t.Logf("EXPECTED FAIL (reference defect): "+format, args...)
		return
	}
	t.Errorf(format, args...)
}

func expect(t *testing.T, r resp, status int, what string) {
	t.Helper()
	if r.Status != status {
		t.Fatalf("%s: want %d, got %d: %.200s", what, status, r.Status, r.Body)
	}
}

func uniq(prefix string) string {
	return fmt.Sprintf("%s-%d", prefix, time.Now().UnixNano()%1_000_000)
}

func TestContractHealthzOpen(t *testing.T) {
	r := call(t, "GET", "/healthz", nil)
	expect(t, r, 200, "GET /healthz")
	body := strings.TrimSpace(string(r.Body))
	if body == "ok" {
		return // эталон TS отдаёт голый ok
	}
	if status, _ := r.JSON["status"].(string); status != "ok" {
		t.Errorf("GET /healthz body: want 200 ok or {\"status\":\"ok\"}, got %.200q", body)
	}
}

func TestContractStateShapeAndMaskedTokens(t *testing.T) {
	r := call(t, "GET", "/api/state", nil)
	expect(t, r, 200, "GET /api/state")
	if _, ok := r.JSON["servers"].([]any); !ok {
		t.Errorf("GET /api/state: servers is not an array: %.200s", r.Body)
	}
	if _, ok := r.JSON["sets"].([]any); !ok {
		t.Errorf("GET /api/state: sets is not an array: %.200s", r.Body)
	}
	// The panel shows a prefix, never a usable key.
	check(t, false, !tokenRE.Match(r.Body), "GET /api/state leaks a raw set token")
}

// GET /api/sets answers a bare array, so the leak is only visible once a set
// actually owns a key.
func TestContractRawSetListLeaksTokens(t *testing.T) {
	srv := uniq("t-key-srv")
	r := call(t, "POST", "/api/servers", map[string]any{
		"name": srv, "transport": "stdio", "command": []string{"echo"},
	})
	expect(t, r, 201, "POST /api/servers "+srv)
	t.Cleanup(func() { call(t, "DELETE", "/api/servers/"+srv, nil) })

	set := uniq("t-key-set")
	r = call(t, "POST", "/api/sets", map[string]any{"name": set, "servers": []string{srv}})
	expect(t, r, 201, "POST /api/sets "+set)
	if id, _ := r.JSON["id"].(string); id != "" {
		t.Cleanup(func() { call(t, "DELETE", "/api/sets/"+id, nil) })
	}

	r = call(t, "GET", "/api/sets", nil)
	expect(t, r, 200, "GET /api/sets")
	check(t, true, !tokenRE.Match(r.Body), "GET /api/sets returns raw token material: %.300s", r.Body)
}

func TestContractUnknownRouteIs404(t *testing.T) {
	r := call(t, "GET", "/no-such-thing", nil)
	expect(t, r, 404, "GET /no-such-thing")
}

func TestContractValidateServer(t *testing.T) {
	cases := []struct {
		name        string
		payload     map[string]any
		knownDefect bool
	}{
		{"uppercase name", map[string]any{"name": "UPPER", "transport": "stdio", "command": []string{"echo"}}, false},
		// The reference validates only that env is an object, not its value
		// types (domain/validation.ts:20-22): a non-string poisons /api/env.
		{"env value not a string", map[string]any{"name": uniq("t-env"), "transport": "stdio", "command": []string{"echo"}, "env": map[string]any{"A": 1}}, true},
		{"stdio without command", map[string]any{"name": uniq("t-nocmd"), "transport": "stdio"}, false},
		{"http without url", map[string]any{"name": uniq("t-nourl"), "transport": "http"}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := call(t, "POST", "/api/servers", c.payload)
			if r.Status == 201 {
				if name, _ := r.JSON["name"].(string); name != "" {
					t.Cleanup(func() { call(t, "DELETE", "/api/servers/"+name, nil) })
				}
			}
			check(t, c.knownDefect, r.Status == 400,
				"POST /api/servers (%s): want 400, got %d: %.200s", c.name, r.Status, r.Body)
		})
	}
}

// The panel publishes 9100-9199, so a set outside that range would start and
// still be unreachable.
func TestContractSetPortRange(t *testing.T) {
	srv := uniq("t-port-srv")
	r := call(t, "POST", "/api/servers", map[string]any{
		"name": srv, "transport": "stdio", "command": []string{"echo"},
	})
	expect(t, r, 201, "POST /api/servers "+srv)
	t.Cleanup(func() { call(t, "DELETE", "/api/servers/"+srv, nil) })

	set := uniq("t-port-set")
	r = call(t, "POST", "/api/sets", map[string]any{
		"name": set, "servers": []string{srv}, "port": 9200,
	})
	if r.Status == 201 {
		id, _ := r.JSON["id"].(string)
		t.Cleanup(func() {
			call(t, "DELETE", "/api/sets/"+id, nil)
		})
	}
	check(t, true, r.Status == 400, "POST /api/sets with port 9200: want 400, got %d", r.Status)
}

func TestContractImportExport(t *testing.T) {
	name := uniq("t-import")
	r := call(t, "POST", "/api/import", map[string]any{
		"mcpServers": map[string]any{name: map[string]any{"command": "echo", "args": []string{"hi"}}},
	})
	expect(t, r, 200, "POST /api/import")
	added, _ := r.JSON["added"].([]any)
	found := false
	for _, v := range added {
		if s, _ := v.(string); s == name {
			found = true
		}
	}
	if !found {
		t.Errorf("POST /api/import: %q missing from added: %.300s", name, r.Body)
	}
	t.Cleanup(func() { call(t, "DELETE", "/api/servers/"+name, nil) })

	r = call(t, "GET", "/api/export/registry", nil)
	expect(t, r, 200, "GET /api/export/registry")
	if tokenRE.Match(r.Body) {
		t.Errorf("GET /api/export/registry exports token material")
	}
}

func TestContractEnvListingHasNoValues(t *testing.T) {
	r := call(t, "GET", "/api/env", nil)
	expect(t, r, 200, "GET /api/env")
	vars, ok := r.JSON["vars"].([]any)
	if !ok {
		t.Fatalf("GET /api/env: vars is not an array: %.200s", r.Body)
	}
	for _, v := range vars {
		item, _ := v.(map[string]any)
		if _, has := item["value"]; has {
			t.Errorf("GET /api/env exposes a value: %.200s", r.Body)
			break
		}
		if _, has := item["name"]; !has {
			t.Errorf("GET /api/env item without name: %.200s", r.Body)
			break
		}
	}
}

// anySetID returns some set, creating server and set when the manager has
// none, so the gate does not need a pre-seeded registry.
func anySetID(t *testing.T) string {
	t.Helper()
	r := call(t, "GET", "/api/sets", nil)
	expect(t, r, 200, "GET /api/sets")
	if sets, ok := r.Any.([]any); ok && len(sets) > 0 {
		if first, ok := sets[0].(map[string]any); ok {
			if id, _ := first["id"].(string); id != "" {
				return id
			}
		}
	}
	srv := uniq("gate-srv")
	r = call(t, "POST", "/api/servers", map[string]any{
		"name": srv, "transport": "stdio", "command": []string{"echo"},
	})
	expect(t, r, 201, "POST /api/servers "+srv)
	t.Cleanup(func() { call(t, "DELETE", "/api/servers/"+srv, nil) })

	name := uniq("gate-set")
	r = call(t, "POST", "/api/sets", map[string]any{"name": name, "servers": []string{srv}})
	expect(t, r, 201, "POST /api/sets "+name)
	id, _ := r.JSON["id"].(string)
	if id == "" {
		t.Fatalf("set created without id: %.200s", r.Body)
	}
	t.Cleanup(func() { call(t, "DELETE", "/api/sets/"+id, nil) })
	return id
}

func TestContractSetLogsShape(t *testing.T) {
	id := anySetID(t)
	r := call(t, "GET", "/api/sets/"+id+"/logs", nil)
	expect(t, r, 200, "GET /api/sets/"+id+"/logs")
	if _, ok := r.JSON["logs"].([]any); !ok {
		t.Errorf("GET set logs: logs is not an array: %.200s", r.Body)
	}
}

func TestContractSetServersHealthShape(t *testing.T) {
	id := anySetID(t)
	r := call(t, "GET", "/api/sets/"+id+"/servers", nil)
	expect(t, r, 200, "GET /api/sets/"+id+"/servers")
	if _, ok := r.JSON["running"].(bool); !ok {
		t.Errorf("set health: running is not a bool: %.200s", r.Body)
	}
	if _, ok := r.JSON["servers"].([]any); !ok {
		t.Errorf("set health: servers is not an array: %.200s", r.Body)
	}
}

// Runs only with E2E_AUTH=1, against a manager started with a password or a
// proxy secret: the API must refuse anonymous callers.
func TestContractAPIRequiresAuth(t *testing.T) {
	if !authOn {
		t.Skip("set E2E_AUTH=1 with a password-protected manager")
	}
	for _, path := range []string{"/api/state", "/api/sets", "/api/servers", "/api/env"} {
		r := call(t, "GET", path, nil)
		check(t, true, r.Status == 401, "GET %s without credentials: want 401, got %d", path, r.Status)
	}
}
