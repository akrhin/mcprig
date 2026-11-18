package manager

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/akrhin/mcprig/internal/registry"
	"github.com/akrhin/mcprig/internal/safelog"
)

const (
	// notifyAttempts — три попытки: падение вебхука не держит событие дольше.
	notifyAttempts = 3
	notifyTimeout  = 5 * time.Second
)

// notifyBackoff — паузы между попытками (100 и 300 мс); третья пауза плана,
// 900 мс, относилась бы к четвёртой попытке, которой нет.
var notifyBackoff = []time.Duration{100 * time.Millisecond, 300 * time.Millisecond}

// notifyEvent — тело POST. Токена, env и команды здесь нет и быть не должно:
// тело уходит на чужой хост.
type notifyEvent struct {
	Event  string `json:"event"`
	Set    string `json:"set"`
	Server string `json:"server,omitempty"`
	Code   int    `json:"code,omitempty"`
	At     string `json:"at"`
}

// notifier шлёт события набора в его вебхук. Отметки переходов живут в памяти
// процесса: после рестарта панели событие уйдёт ещё раз — это дешевле файла с
// состоянием, который пережил бы и правку реестра.
type notifier struct {
	log    *slog.Logger
	client *http.Client
	wg     sync.WaitGroup

	mu   sync.Mutex
	last map[string]string // "набор/сервер" → последнее отправленное событие
}

func newNotifier(log *slog.Logger) *notifier {
	return &notifier{
		log:    log,
		client: &http.Client{Timeout: notifyTimeout},
		last:   map[string]string{},
	}
}

// webhookOf — адрес уведомлений набора.
func webhookOf(set *registry.Set) string {
	if set == nil || set.Notify == nil {
		return ""
	}
	return set.Notify.Webhook
}

// emit отправляет событие перехода, если ключ ещё не в этом состоянии. Сеть
// уходит в свою горутину со своим контекстом: набор её не ждёт.
func (n *notifier) emit(webhook, setID, server, event string, code int) {
	if n == nil || webhook == "" || event == "" {
		return
	}
	key := setID + "/" + server
	n.mu.Lock()
	if n.last[key] == event {
		n.mu.Unlock()
		return
	}
	n.last[key] = event
	n.mu.Unlock()

	ev := notifyEvent{Event: event, Set: setID, Server: server, Code: code, At: time.Now().UTC().Format(time.RFC3339)}
	n.wg.Add(1)
	go func() {
		defer n.wg.Done()
		if err := n.send(webhook, ev); err != nil {
			// Не доставлено — переход не считается отправленным: следующее то
			// же событие уйдёт снова. Метку снимаем, только если её не
			// переписал более новый переход.
			n.mu.Lock()
			if n.last[key] == event {
				delete(n.last, key)
			}
			n.mu.Unlock()
		}
	}()
}

// clear забывает переход: набор или сервер снова здоров, следующая поломка —
// новый переход и новое событие.
func (n *notifier) clear(setID, server string) {
	if n == nil {
		return
	}
	n.mu.Lock()
	delete(n.last, setID+"/"+server)
	n.mu.Unlock()
}

// clearSet забывает все переходы набора сразу: удалённый набор не должен
// навсегда подавлять события нового набора с тем же id.
func (n *notifier) clearSet(setID string) {
	if n == nil {
		return
	}
	n.mu.Lock()
	for key := range n.last {
		if strings.HasPrefix(key, setID+"/") {
			delete(n.last, key)
		}
	}
	n.mu.Unlock()
}

// drain ждёт отправок в полёте; нужен тестам, чтобы горутины успели кончиться.
func (n *notifier) drain() {
	if n != nil {
		n.wg.Wait()
	}
}

func (n *notifier) send(webhook string, ev notifyEvent) error {
	body, err := json.Marshal(ev)
	if err != nil {
		return err
	}
	var lastErr error
	for attempt := 0; attempt < notifyAttempts; attempt++ {
		if attempt > 0 {
			time.Sleep(notifyBackoff[attempt-1])
		}
		if lastErr = n.post(webhook, body); lastErr == nil {
			return nil
		}
	}
	// Адрес вебхука в лог не идёт: в нём бывает собственный токен, а post
	// уже снял адрес с ошибки транспорта.
	n.log.Warn("вебхук набора не отправлен", "set", safelog.Value(ev.Set), "event", safelog.Value(ev.Event), "err", lastErr)
	return lastErr
}

// redactURL снимает адрес с ошибки транспорта: *url.Error печатает полный
// URL вместе с query, а там бывает токен вебхука.
func redactURL(err error) error {
	var uerr *url.Error
	if errors.As(err, &uerr) && uerr.Err != nil {
		return uerr.Err
	}
	return errors.New("развернуть *url.Error до причины; адрес в лог не идёт")
}

func (n *notifier) post(webhook string, body []byte) error {
	u, err := url.Parse(webhook)
	if err != nil {
		return redactURL(err)
	}
	// Схема, хост и учётные данные проверяются до запроса: адрес приходит из
	// реестра, а file:// или user:pass@ увели бы запрос мимо вебхука.
	if (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil {
		return errors.New("вебхук: нужен адрес http:// или https:// с хостом, без user:pass@")
	}
	ctx, cancel := context.WithTimeout(context.Background(), notifyTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), bytes.NewReader(body))
	if err != nil {
		return redactURL(err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := n.client.Do(req)
	if err != nil {
		return redactURL(err)
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	_ = resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Errorf("вебхук ответил %d", resp.StatusCode)
	}
	return nil
}
