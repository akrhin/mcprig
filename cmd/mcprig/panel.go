package main

// Вход в панель, общий для warm, status и logs: адрес, пароль или секрет
// обратного прокси, cookie-сессия.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"strings"
	"time"
)

// panelCreds — поля входа в панель; флаги warm, status и logs повторяют их.
type panelCreds struct {
	Password string
	Secret   string
	User     string
}

// panelBaseURL: --panel, иначе MCPRIG_PANEL_URL, иначе локальная панель на
// MCPRIG_PORT (9091 по умолчанию, как в deploy/mcprig.service).
func panelBaseURL(panel string) string {
	if panel != "" {
		return panel
	}
	if v := os.Getenv("MCPRIG_PANEL_URL"); v != "" {
		return v
	}
	return "http://127.0.0.1:" + envDefault("MCPRIG_PORT", "9091")
}

// newPanelClient — клиент панели с cookie jar. Пароль задан — входит через
// /api/login, и дальше ходит с сессией; пустой — рассчитывает на обратный
// прокси, которому хватает panelHeaders.
func newPanelClient(ctx context.Context, base string, c panelCreds) (*http.Client, error) {
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar, Timeout: 10 * time.Second}
	password := c.Password
	if password == "" {
		password = os.Getenv("MCPRIG_PASSWORD")
	}
	if password == "" {
		return client, nil
	}
	user := c.User
	if user == "" {
		user = "admin"
	}
	body, _ := json.Marshal(map[string]string{"user": user, "password": password})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/api/login", strings.NewReader(string(body)))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	panelHeaders(req, c)
	res, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("панель %s: %w", base, err)
	}
	raw, _ := io.ReadAll(io.LimitReader(res.Body, 4096))
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK {
		fmt.Fprintf(os.Stderr, "ответ панели: %s\n", strings.TrimSpace(string(raw)))
		return nil, fmt.Errorf("панель %s не приняла вход: HTTP %d", base, res.StatusCode)
	}
	return client, nil
}

// panelGet — GET панели с заголовками входа; what называет раздел в ошибке.
func panelGet(ctx context.Context, client *http.Client, base string, c panelCreds, path, what string) ([]byte, error) {
	raw, code, err := panelDo(ctx, client, base, c, path)
	if err != nil {
		return nil, err
	}
	if code != http.StatusOK {
		fmt.Fprintf(os.Stderr, "ответ панели: %s\n", strings.TrimSpace(string(raw)))
		return nil, fmt.Errorf("панель %s не отдала %s: HTTP %d", base, what, code)
	}
	return raw, nil
}

// panelDo — GET без разбора кода: необязательный запрос молчит об отказе,
// обязательный решает сам (panelGet).
func panelDo(ctx context.Context, client *http.Client, base string, c panelCreds, path string) ([]byte, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+path, nil)
	if err != nil {
		return nil, 0, err
	}
	panelHeaders(req, c)
	req.Header.Set("Origin", panelOrigin(base))
	res, err := client.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("панель %s: %w", base, err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(res.Body, 8<<20))
	return raw, res.StatusCode, nil
}

// panelPost — POST панели с JSON-телом. Не-200 — ошибка с ответом панели:
// молчащий отказ здесь значил бы «одобрено», которого не было.
func panelPost(ctx context.Context, client *http.Client, base string, c panelCreds, path string, body any) ([]byte, error) {
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+path, strings.NewReader(string(raw)))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	panelHeaders(req, c)
	// checkCSRF отбивает POST с сессионной cookie без Origin: браузер шлёт его
	// всегда, и CLI обязан вести себя как браузер.
	req.Header.Set("Origin", panelOrigin(base))
	res, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("панель %s: %w", base, err)
	}
	defer res.Body.Close()
	answer, _ := io.ReadAll(io.LimitReader(res.Body, 8<<20))
	if res.StatusCode != http.StatusOK {
		fmt.Fprintf(os.Stderr, "ответ панели: %s\n", strings.TrimSpace(string(answer)))
		return nil, fmt.Errorf("панель %s не приняла %s: HTTP %d", base, path, res.StatusCode)
	}
	return answer, nil
}

// panelOrigin — Origin запроса к панели: схема и хост её адреса. Панель
// сверяет его со своим хостом, поэтому подставить адрес из флага мало — нужен
// именно источник запроса.
func panelOrigin(base string) string {
	if u, err := url.Parse(base); err == nil && u.Host != "" {
		return u.Scheme + "://" + u.Host
	}
	return base
}

// panelHeaders — вход через обратный прокси: секрет и имя пользователя.
func panelHeaders(req *http.Request, c panelCreds) {
	secret := c.Secret
	if secret == "" {
		secret = os.Getenv("MCPRIG_PROXY_SECRET")
	}
	if secret == "" {
		return
	}
	req.Header.Set("x-mcp-proxy-secret", secret)
	user := c.User
	if user == "" {
		user = "admin"
	}
	req.Header.Set(envDefault("MCPRIG_USER_HEADER", "Remote-User"), user)
}
