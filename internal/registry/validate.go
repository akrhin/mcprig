package registry

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/akrhin/mcprig/internal/config"
	"github.com/akrhin/mcprig/internal/safepath"
)

// ErrPortBusy — порт занят другим набором или процессом; вызывающий отличает её
// через errors.Is, не разбирая сообщение. Текст ошибки при этом прежний, поэтому
// метку несёт тип, а не приписка к сообщению.
var ErrPortBusy = errors.New("порт занят")

type busyErr struct{ msg string }

func (e busyErr) Error() string        { return e.msg }
func (e busyErr) Is(target error) bool { return target == ErrPortBusy }

// ErrInvalid — тело запроса не прошло проверку реестра: API отвечает 400 с
// причиной, а не 500. Сбои диска и записи этой метки не несут.
var ErrInvalid = errors.New("неверные данные реестра")

// invalidErr несёт текст причины как есть: клиенту нужна она, а не слово «ошибка».
type invalidErr struct{ msg string }

func (e invalidErr) Error() string        { return e.msg }
func (e invalidErr) Is(target error) bool { return target == ErrInvalid }

func invalidf(format string, args ...any) error {
	return invalidErr{fmt.Sprintf(format, args...)}
}

func isNull(raw json.RawMessage) bool {
	trimmed := bytes.TrimSpace(raw)
	return len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null"))
}

// decodeObject reads a JSON object without losing the raw field values, so a
// present field can be told from an absent one.
func decodeObject(raw json.RawMessage) (map[string]json.RawMessage, error) {
	if isNull(raw) {
		return nil, invalidf("ожидался JSON-объект")
	}
	var out map[string]json.RawMessage
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, invalidf("ожидался JSON-объект")
	}
	return out, nil
}

func optionalString(in map[string]json.RawMessage, key string) (string, error) {
	raw, ok := in[key]
	if !ok || isNull(raw) {
		return "", nil
	}
	var v string
	if err := json.Unmarshal(raw, &v); err != nil {
		return "", invalidf("%s: ожидалась строка", key)
	}
	return strings.TrimSpace(v), nil
}

func requiredString(in map[string]json.RawMessage, key string) (string, error) {
	v, err := optionalString(in, key)
	if err != nil {
		return "", err
	}
	if v == "" {
		return "", invalidf("%s: обязательное поле — заполните его", key)
	}
	return v, nil
}

// stringMapStrict is the reference defect fixed: a non-string value in env or
// headers is an error, never a silently stringified object.
func stringMapStrict(in map[string]json.RawMessage, key string) (map[string]string, error) {
	raw, ok := in[key]
	if !ok || isNull(raw) {
		return nil, nil
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		return nil, invalidf("%s: ожидался объект", key)
	}
	if len(obj) == 0 {
		return nil, nil
	}
	out := make(map[string]string, len(obj))
	for k, v := range obj {
		var str string
		if err := json.Unmarshal(v, &str); err != nil {
			return nil, invalidf("%s.%s: ожидалась строка", key, k)
		}
		out[k] = str
	}
	return out, nil
}

func stringSliceStrict(in map[string]json.RawMessage, key string) ([]string, error) {
	raw, ok := in[key]
	if !ok || isNull(raw) {
		return nil, nil
	}
	var out []string
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, invalidf("%s: ожидался массив строк", key)
	}
	return out, nil
}

// commandOf accepts both command forms: an argv array, or a string naming the
// executable (its arguments come only from the legacy `args` array, like the
// TS reference). Никто не режет строку по пробелам: MCPHub-сокращение в
// импорте отклоняется как неоднозначное (кавычки в пути ломают argv).
func commandOf(in map[string]json.RawMessage) ([]string, error) {
	var command []string
	if raw, ok := in["command"]; ok && !isNull(raw) {
		if err := json.Unmarshal(raw, &command); err != nil {
			var one string
			if err := json.Unmarshal(raw, &one); err != nil {
				return nil, invalidf("command: ожидалась строка или массив строк")
			}
			command = []string{one}
		}
	}
	args, err := stringSliceStrict(in, "args")
	if err != nil {
		return nil, err
	}
	return append(command, args...), nil
}

// parseServer validates one server entry strictly. `name` is the registry key
// and never comes from the body.
func parseServer(name string, in map[string]json.RawMessage) (Server, error) {
	if !safepath.Name(name) {
		return nil, invalidf("в имени допустимы строчные латинские буквы, цифры, дефис и подчёркивание")
	}
	srv := &config.Server{Name: name, Enabled: true}

	transport, err := optionalString(in, "transport")
	if err != nil {
		return nil, err
	}
	if transport != "" && transport != "stdio" && transport != "http" {
		return nil, invalidf("транспорт: только stdio или http")
	}
	command, err := commandOf(in)
	if err != nil {
		return nil, err
	}
	url, err := optionalString(in, "url")
	if err != nil {
		return nil, err
	}
	description, err := optionalString(in, "description")
	if err != nil {
		return nil, err
	}
	hint, err := optionalString(in, "hint")
	if err != nil {
		return nil, err
	}
	cwd, err := optionalString(in, "cwd")
	if err != nil {
		return nil, err
	}
	env, err := stringMapStrict(in, "env")
	if err != nil {
		return nil, err
	}
	envAllow, err := stringSliceStrict(in, "envAllow")
	if err != nil {
		return nil, err
	}
	// sandbox и envAllow приходят тем же JSON, что читает config.Load: без
	// переноса сервер из панели молча терял изоляцию.
	var sandbox *config.Sandbox
	if raw, ok := in["sandbox"]; ok && !isNull(raw) {
		var sb config.Sandbox
		if err := json.Unmarshal(raw, &sb); err != nil {
			return nil, invalidf("sandbox: ожидался объект")
		}
		if unknown := config.UnknownSandboxKeys(raw); len(unknown) > 0 {
			return nil, invalidf("sandbox: неизвестные поля: %s", strings.Join(unknown, ", "))
		}
		sandbox = &sb
	}
	headers, err := stringMapStrict(in, "headers")
	if err != nil {
		return nil, err
	}
	tags, err := stringSliceStrict(in, "tags")
	if err != nil {
		return nil, err
	}
	// permissions читается тем же разбором, что и config.Load: сервер из панели
	// иначе оставался без правил, и browse с invoke_command разрешали всё.
	var perms *config.Permissions
	if raw, ok := in["permissions"]; ok && !isNull(raw) {
		p, msg := config.ParsePermissions(raw)
		if msg != "" {
			return nil, invalidf("%s", msg)
		}
		perms = p
	}
	// expose читается тем же разбором, что и config.Load: без него сервер из
	// панели не отдавал клиенту ни ресурсов, ни промптов, и включить это было
	// неоткуда. Неизвестный вид ловит Check ниже — одним списком допустимых.
	var expose []string
	if raw, ok := in["expose"]; ok && !isNull(raw) {
		list, msg := config.ParseExpose(raw)
		if msg != "" {
			return nil, invalidf("%s", msg)
		}
		expose = list
	}

	hasCommand, hasURL := len(command) > 0, url != ""
	if hasCommand && hasURL {
		return nil, invalidf("нужен ровно один из них: command[] или url")
	}
	if !hasCommand && !hasURL {
		return nil, invalidf("нужен ровно один из них: command[] или url")
	}
	if transport == "" {
		if hasURL {
			transport = "http"
		} else {
			transport = "stdio"
		}
	}
	switch transport {
	case "stdio":
		if !hasCommand {
			return nil, invalidf("для транспорта stdio нужен command[]")
		}
	case "http":
		if !hasURL {
			return nil, invalidf("для транспорта http нужен url")
		}
		if _, ok := in["cwd"]; ok && !isNull(in["cwd"]) {
			return nil, invalidf("cwd допустим только для транспорта stdio")
		}
	}

	enabled := true
	if raw, ok := in["enabled"]; ok && !isNull(raw) {
		if err := json.Unmarshal(raw, &enabled); err != nil {
			return nil, invalidf("enabled: ожидалось true или false")
		}
	}

	srv.Transport = config.TransportKind(transport)
	srv.Description = description
	srv.Hint = hint
	srv.Command = command
	srv.URL = url
	srv.Cwd = cwd
	srv.Env = env
	srv.EnvAllow = envAllow
	srv.Headers = headers
	srv.Tags = tags
	srv.Enabled = enabled
	srv.Sandbox = sandbox
	srv.Permissions = perms
	srv.Expose = expose
	// Те же правила, что у config.Load, включая запрет untrusted без песочницы:
	// иначе API отвечает 200 и сервер живёт без изоляции.
	if msg := srv.Check(); msg != "" {
		return nil, invalidf("%s", msg)
	}
	return srv, nil
}

// mergeServer overlays a partial patch on the current entry: absent fields
// stay, explicit null removes one (that is how a transport is switched).
func mergeServer(cur Server, patch map[string]json.RawMessage) (map[string]json.RawMessage, error) {
	raw, err := json.Marshal(cur)
	if err != nil {
		return nil, err
	}
	out, err := decodeObject(raw)
	if err != nil {
		return nil, err
	}
	for k, v := range patch {
		if isNull(v) {
			delete(out, k)
			continue
		}
		out[k] = v
	}
	name, err := json.Marshal(cur.Name)
	if err != nil {
		return nil, err
	}
	out["name"] = name
	return out, nil
}

func checkPort(port int) error {
	if port < FirstPort || port > LastPort {
		return invalidf("порт должен быть целым числом в диапазоне %d–%d", FirstPort, LastPort)
	}
	return nil
}

func strictBool(in map[string]json.RawMessage, key string) (value, present bool, err error) {
	raw, ok := in[key]
	if !ok || isNull(raw) {
		return false, false, nil
	}
	var b bool
	if err := json.Unmarshal(raw, &b); err != nil {
		return false, true, invalidf("%s: ожидалось true или false", key)
	}
	return b, true, nil
}

// buildSet validates a set from a create body (cur == nil) or a partial update
// (cur is the stored set; id and keys are never taken from the body).
func (s *Store) buildSet(cur *Set, in map[string]json.RawMessage) (*Set, error) {
	out := &Set{}
	if cur != nil {
		*out = *cur
		out.Servers = append([]string(nil), cur.Servers...)
		out.Keys = append([]Key(nil), cur.Keys...)
	}

	if cur == nil {
		id, err := optionalString(in, "id")
		if err != nil {
			return nil, err
		}
		if id == "" {
			if id, err = optionalString(in, "name"); err != nil {
				return nil, err
			}
		}
		// имя больше не подгоняется молча: «My Set» и «my set» — разные
		// намерения, и набор под чужим id удивлял бы сильнее, чем отказ
		out.ID = strings.ToLower(strings.TrimSpace(id))
		if !safepath.Name(out.ID) {
			return nil, invalidf("имя набора: строчные латинские буквы, цифры, дефис и подчёркивание")
		}
	}

	if name, err := optionalString(in, "name"); err != nil {
		return nil, err
	} else if name != "" {
		out.Name = name
	}
	if out.Name == "" {
		out.Name = out.ID
	}
	if description, err := optionalString(in, "description"); err != nil {
		return nil, err
	} else if description != "" {
		out.Description = description
	}

	if raw, ok := in["servers"]; ok && !isNull(raw) {
		var names []string
		if err := json.Unmarshal(raw, &names); err != nil {
			return nil, invalidf("servers: ожидался массив имён")
		}
		var missing []string
		seen := make(map[string]bool, len(names))
		for _, name := range names {
			if s.serverLocked(name) == nil {
				missing = append(missing, name)
			}
			if seen[name] {
				// Дубль в составе — не опечатка в ответе, а второй экземпляр
				// того же сервера: набор откроет его дважды и подменит ключи.
				return nil, invalidf("сервер %q указан в наборе дважды", name)
			}
			seen[name] = true
		}
		if len(missing) > 0 {
			return nil, invalidf("в реестре нет серверов: %s", strings.Join(missing, ", "))
		}
		out.Servers = names
	}
	// Пустой состав — законное состояние набора (решение 0004): ключ и порт
	// живут отдельно. nil здесь значит "без серверов", а не "не менять": выше
	// правка со старым составом уже скопирована из cur.
	if out.Servers == nil {
		out.Servers = []string{}
	}

	explicitPort := false
	if raw, ok := in["port"]; ok && !isNull(raw) {
		var port int
		if err := json.Unmarshal(raw, &port); err != nil {
			return nil, invalidf("порт должен быть целым числом в диапазоне %d–%d", FirstPort, LastPort)
		}
		out.Port = port
		explicitPort = true
	}
	if out.Port == 0 {
		port, err := s.allocPortLocked()
		if err != nil {
			return nil, err
		}
		out.Port = port
	}
	if err := checkPort(out.Port); err != nil {
		return nil, err
	}
	for _, other := range s.data.Sets {
		if other.ID != out.ID && other.Port == out.Port {
			return nil, busyErr{fmt.Sprintf("порт %d занят набором %s", out.Port, other.ID)}
		}
	}
	// Порт из тела при правке: незанятый в реестре ещё не значит свободный в
	// системе, иначе рестарт набора упадёт уже после записи.
	if cur != nil && explicitPort && cur.Port != out.Port && portBusy(out.Port) {
		return nil, busyErr{fmt.Sprintf("порт %d занят процессом — выберите другой", out.Port)}
	}

	if autostart, present, err := strictBool(in, "autostart"); err != nil {
		return nil, err
	} else if present {
		out.Autostart = autostart
	}
	if open, present, err := strictBool(in, "open"); err != nil {
		return nil, err
	} else if present {
		out.Open = open
	}

	// notify — три состояния: поля нет — старое значение остаётся; null —
	// снятие; объект — замена (пустой webhook внутри объекта — тоже снятие,
	// иначе правка не могла бы убрать блок, не зная про null).
	if raw, ok := in["notify"]; ok {
		if isNull(raw) {
			out.Notify = nil
		} else {
			var n Notify
			if err := json.Unmarshal(raw, &n); err != nil {
				return nil, invalidf(`notify: ожидался объект {"webhook": "https://…"}`)
			}
			if msg := n.Check(); msg != "" {
				return nil, invalidf("%s", msg)
			}
			if strings.TrimSpace(n.Webhook) == "" {
				out.Notify = nil
			} else {
				out.Notify = &n
			}
		}
	}

	// rateLimit — те же три состояния, что у notify: поля нет — значение
	// остаётся; null — снятие; объект — замена. Нулевой блок значит «без
	// лимита» и хранится как отсутствие поля.
	if raw, ok := in["rateLimit"]; ok {
		if isNull(raw) {
			out.RateLimit = nil
		} else {
			var rl config.RateLimit
			if err := rl.Decode(raw); err != nil {
				return nil, invalidf(`rateLimit: ожидался объект {"perMinute": 120, "burst": 20, "perTool": {"server/tool": {"perMinute": 10, "burst": 2}}}: %v`, err)
			}
			if msg := rl.Check(); msg != "" {
				return nil, invalidf("%s", msg)
			}
			if rl.Empty() {
				out.RateLimit = nil
			} else {
				out.RateLimit = &rl
			}
		}
	}

	// profiles — те же три состояния, что у notify: поля нет — блок остаётся,
	// null — снятие, объект — замена целиком. Ключи, назвавшие снятый профиль,
	// перестают проходить вызовы: профиль не находится — отказ, а не «всё можно».
	if raw, ok := in["profiles"]; ok {
		if isNull(raw) {
			out.Profiles = nil
		} else {
			profiles, err := config.ParseProfiles(raw)
			if err != nil {
				return nil, invalidf("%s", err)
			}
			out.Profiles = profiles
		}
	}

	// token — совместимость: PUT с "token" назначает токен основному ключу,
	// а если действующих ключей нет — заводит ключ с новым id. Токены
	// остальных ключей это не трогает: их выдаёт и отзывает /keys.
	if raw, ok := in["token"]; ok && !isNull(raw) {
		var token string
		if err := json.Unmarshal(raw, &token); err != nil {
			return nil, invalidf("token: ожидался токен строкой")
		}
		token = strings.TrimSpace(token)
		if token != "" {
			primary := -1
			for i, key := range out.Keys {
				if key.RevokedAt == "" {
					primary = i
					break
				}
			}
			if primary < 0 {
				out.Keys = append(out.Keys, Key{ID: freshKeyID(out.Keys), Token: token, CreatedAt: time.Now().UTC().Format(time.RFC3339)})
			} else {
				out.Keys[primary].Token = token
			}
		}
	}

	// normalizeKeys до записи: фиксированный id нового ключа столкнулся бы с
	// отозванным ключом той же истории, и загрузка оставила бы отозванный —
	// показанный владельцу токен молча перестал бы работать после перезапуска.
	out.Keys = normalizeKeys(out.Keys, "", time.Now().UTC().Format(time.RFC3339))
	out.Token = activeToken(out.Keys)
	return out, nil
}
