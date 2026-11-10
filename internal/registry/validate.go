package registry

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/sintez/mcprig/internal/config"
)

var namePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)

func isNull(raw json.RawMessage) bool {
	trimmed := bytes.TrimSpace(raw)
	return len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null"))
}

// decodeObject reads a JSON object without losing the raw field values, so a
// present field can be told from an absent one.
func decodeObject(raw json.RawMessage) (map[string]json.RawMessage, error) {
	if isNull(raw) {
		return nil, errors.New("ожидался JSON-объект")
	}
	var out map[string]json.RawMessage
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, errors.New("ожидался JSON-объект")
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
		return "", fmt.Errorf("%s: ожидалась строка", key)
	}
	return strings.TrimSpace(v), nil
}

func requiredString(in map[string]json.RawMessage, key string) (string, error) {
	v, err := optionalString(in, key)
	if err != nil {
		return "", err
	}
	if v == "" {
		return "", fmt.Errorf("%s: обязательное поле", key)
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
		return nil, fmt.Errorf("%s: ожидался объект", key)
	}
	if len(obj) == 0 {
		return nil, nil
	}
	out := make(map[string]string, len(obj))
	for k, v := range obj {
		var str string
		if err := json.Unmarshal(v, &str); err != nil {
			return nil, fmt.Errorf("%s.%s: ожидалась строка", key, k)
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
		return nil, fmt.Errorf("%s: ожидался массив строк", key)
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
				return nil, errors.New("command: ожидалась строка или массив строк")
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
	if !namePattern.MatchString(name) {
		return nil, errors.New("имя сервера: строчные латинские буквы, цифры, дефис и подчёркивание")
	}
	srv := &config.Server{Name: name, Enabled: true}

	transport, err := optionalString(in, "transport")
	if err != nil {
		return nil, err
	}
	if transport != "" && transport != "stdio" && transport != "http" {
		return nil, errors.New("транспорт: только stdio или http")
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
			return nil, fmt.Errorf("sandbox: ожидался объект")
		}
		if unknown := config.UnknownSandboxKeys(raw); len(unknown) > 0 {
			return nil, fmt.Errorf("sandbox: неизвестные поля: %s", strings.Join(unknown, ", "))
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

	hasCommand, hasURL := len(command) > 0, url != ""
	if hasCommand && hasURL {
		return nil, errors.New("укажите что-то одно: command[] или url")
	}
	if !hasCommand && !hasURL {
		return nil, errors.New("нужен command[] или url")
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
			return nil, errors.New("транспорт stdio требует command[]")
		}
	case "http":
		if !hasURL {
			return nil, errors.New("транспорт http требует url")
		}
		if _, ok := in["cwd"]; ok && !isNull(in["cwd"]) {
			return nil, errors.New("cwd допустим только для транспорта stdio")
		}
	}

	enabled := true
	if raw, ok := in["enabled"]; ok && !isNull(raw) {
		if err := json.Unmarshal(raw, &enabled); err != nil {
			return nil, errors.New("enabled: ожидалось true или false")
		}
	}

	srv.Transport = config.TransportKind(transport)
	srv.Description = description
	srv.Command = command
	srv.URL = url
	srv.Cwd = cwd
	srv.Env = env
	srv.EnvAllow = envAllow
	srv.Headers = headers
	srv.Tags = tags
	srv.Enabled = enabled
	srv.Sandbox = sandbox
	// Те же правила, что у config.Load, включая запрет untrusted без песочницы:
	// иначе API отвечает 200 и сервер живёт без изоляции.
	if msg := srv.Check(); msg != "" {
		return nil, errors.New(msg)
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
	delete(out, "_configError")
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

// slugify turns an id or name into a set id the name pattern accepts.
func slugify(v string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(strings.TrimSpace(v)) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '_':
			b.WriteRune(r)
			dash = false
		default:
			if !dash {
				b.WriteByte('-')
				dash = true
			}
		}
	}
	return strings.Trim(b.String(), "-")
}

func checkPort(port int) error {
	if port < FirstPort || port > LastPort {
		return fmt.Errorf("порт: целое %d–%d", FirstPort, LastPort)
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
		return false, true, fmt.Errorf("%s: ожидалось true или false", key)
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
		out.ID = slugify(id)
		if !namePattern.MatchString(out.ID) {
			return nil, errors.New("имя набора: строчные латинские буквы, цифры, дефис и подчёркивание")
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
			return nil, errors.New("servers: ожидался массив имён")
		}
		if len(names) == 0 {
			return nil, errors.New("в наборе должен быть хотя бы один сервер")
		}
		var missing []string
		for _, name := range names {
			if s.serverLocked(name) == nil {
				missing = append(missing, name)
			}
		}
		if len(missing) > 0 {
			return nil, fmt.Errorf("нет в реестре: %s", strings.Join(missing, ", "))
		}
		out.Servers = names
	}
	if len(out.Servers) == 0 {
		return nil, errors.New("в наборе должен быть хотя бы один сервер")
	}

	explicitPort := false
	if raw, ok := in["port"]; ok && !isNull(raw) {
		var port int
		if err := json.Unmarshal(raw, &port); err != nil {
			return nil, fmt.Errorf("порт: целое %d–%d", FirstPort, LastPort)
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
			return nil, fmt.Errorf("порт %d занят набором %s", out.Port, other.ID)
		}
	}
	// Порт из тела при правке: незанятый в реестре ещё не значит свободный в
	// системе, иначе рестарт набора упадёт уже после записи.
	if cur != nil && explicitPort && cur.Port != out.Port && portBusy(out.Port) {
		return nil, fmt.Errorf("порт %d занят процессом", out.Port)
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

	out.Token = activeToken(out.Keys)
	return out, nil
}
