package manager

import (
	"bytes"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

// Занятый порт — 409 с прежним текстом: это состояние, а не кривой запрос.
func TestStartSetBusyPortConflict(t *testing.T) {
	e := newEnv(t, AuthOptions{Off: true})
	e.addServer(t, "a")
	id := e.addSet(t, "s", "a")
	port := e.m.store.Set(id).Port
	ln, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if err != nil {
		t.Fatalf("занять порт %d: %v", port, err)
	}
	defer ln.Close()

	w := e.do(t, http.MethodPost, "/api/sets/"+id+"/start", nil)
	status(t, w, http.StatusConflict)
	if msg := decode[map[string]string](t, w)["error"]; !strings.Contains(msg, "занят другим процессом") {
		t.Fatalf("ошибка = %q", msg)
	}
}

// «Набор не поднялся» — тот же 409: ожидание 15 с прогрева в тесте не
// воспроизводится, проверяется маппинг типизированной ошибки.
func TestFailSetNotStartedConflict(t *testing.T) {
	e := newEnv(t, AuthOptions{Off: true})
	w := httptest.NewRecorder()
	e.m.fail(w, httptest.NewRequest(http.MethodPost, "/api/sets/s/start", nil),
		conflictf("набор %s не поднялся: порт %d не слушает", "s", 9101))
	status(t, w, http.StatusConflict)
	if !strings.Contains(w.Body.String(), "не поднялся") {
		t.Fatalf("ответ = %s", w.Body.String())
	}
}

// Неизвестная ошибка — 500 с обезличенным телом, причина только в логе.
func TestFailUnknownErrorIs500AndLogged(t *testing.T) {
	var buf bytes.Buffer
	e := newEnvLog(t, AuthOptions{Off: true}, slog.New(slog.NewTextHandler(&buf, nil)))
	w := httptest.NewRecorder()
	e.m.fail(w, httptest.NewRequest(http.MethodGet, "/api/sets", nil), errors.New("внутренности: пароль с3крет"))

	status(t, w, http.StatusInternalServerError)
	if body := w.Body.String(); strings.Contains(body, "с3крет") {
		t.Fatalf("текст ошибки ушёл клиенту: %s", body)
	}
	if !strings.Contains(buf.String(), "с3крет") {
		t.Fatalf("в логе нет причины: %s", buf.String())
	}
}

// Неразрешённая ${VAR} отбивает старт с именем переменной.
func TestStartSetUnresolvedVarRefused(t *testing.T) {
	e := newEnv(t, AuthOptions{Off: true})
	e.addServerWithEnv(t, "db", map[string]string{"PASSWORD": "${TEST_ABSENT_VAR}"})
	id := e.addSet(t, "s", "db")

	w := e.do(t, http.MethodPost, "/api/sets/"+id+"/start", nil)
	status(t, w, http.StatusConflict)
	msg := decode[map[string]string](t, w)["error"]
	if !strings.Contains(msg, "TEST_ABSENT_VAR") || !strings.Contains(msg, "источник не найден") {
		t.Fatalf("ошибка = %q", msg)
	}
	if st := e.m.rt.status(e.m.store.Set(id)); st.Running || st.PID != 0 {
		t.Fatalf("набор запущен без переменной: %+v", st)
	}
}

// Переменная, объявленная пустой намеренно, старт не блокирует.
func TestStartSetAllowsEmptyVar(t *testing.T) {
	t.Setenv("TEST_EMPTY_VAR", "")
	e := newEnv(t, AuthOptions{Off: true})
	e.addServerWithEnv(t, "db", map[string]string{"PASSWORD": "${TEST_EMPTY_VAR}"})
	id := e.addSet(t, "s", "db")

	status(t, e.do(t, http.MethodPost, "/api/sets/"+id+"/start", nil), http.StatusOK)
	e.do(t, http.MethodPost, "/api/sets/"+id+"/stop", nil)
}
