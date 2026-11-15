package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

// Profile — именованный набор глоб-правил, который сужает права ключа набора.
// Профиль применяется после permissions и только запрещает: deny побеждает
// allow, поэтому профиль не может разрешить то, что запретили permissions.
type Profile struct {
	Deny  []string `json:"deny,omitempty"`
	Allow []string `json:"allow,omitempty"`
}

// Allows reports whether the profile lets one server/command through. A
// pattern with "/" matches "server/command" (rateLimit's form); one without it
// matches the command name alone, like permissions rules.
func (p *Profile) Allows(server, command string) bool {
	if p == nil {
		return true
	}
	target := server + "/" + command
	for _, pattern := range p.Deny {
		if profileMatch(pattern, command, target) {
			return false
		}
	}
	if len(p.Allow) == 0 {
		return true
	}
	for _, pattern := range p.Allow {
		if profileMatch(pattern, command, target) {
			return true
		}
	}
	return false
}

func profileMatch(pattern, command, target string) bool {
	if strings.Contains(pattern, "/") {
		return globMatch(pattern, target)
	}
	return globMatch(pattern, command)
}

// Empty reports whether the profile cannot deny anything: no deny rules, and
// no allow list that would act as a whitelist of nothing.
func (p *Profile) Empty() bool {
	return p == nil || (len(p.Deny) == 0 && len(p.Allow) == 0)
}

// ParseProfiles decodes a profiles block. Unknown fields and empty patterns are
// errors: "deny": ["*"] typo'd as "denny" would otherwise silently deny nothing.
func ParseProfiles(raw []byte) (map[string]*Profile, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil, nil
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var out map[string]*Profile
	if err := dec.Decode(&out); err != nil {
		return nil, fmt.Errorf("profiles: ожидался объект {\"reader\": {\"deny\": [\"*/*_write\"], \"allow\": [\"*\"]}}: %w", err)
	}
	// Имя нормализуется по краям: keyProfileOf обрезает имя из запроса, и без
	// этого профиль " reader " существовал бы в наборе, но выдать его было бы
	// нельзя. Два имени, слипшихся после обрезки, — ошибка, а не тихий выбор.
	named := make(map[string]*Profile, len(out))
	for name, p := range out {
		key := strings.TrimSpace(name)
		if key == "" {
			return nil, fmt.Errorf("profiles: пустое имя профиля")
		}
		if _, dup := named[key]; dup {
			return nil, fmt.Errorf("profiles: имя %q встречается дважды после обрезки пробелов по краям", key)
		}
		if p == nil {
			p = &Profile{}
		}
		if msg := p.Check(key); msg != "" {
			return nil, fmt.Errorf("%s", msg)
		}
		named[key] = p
	}
	if len(named) == 0 {
		return nil, nil
	}
	return named, nil
}

// Check returns a defect description, empty when the profile is usable.
func (p *Profile) Check(name string) string {
	if p == nil {
		return name + ": ожидался объект {\"deny\": […], \"allow\": […]}"
	}
	for i, pattern := range p.Deny {
		if strings.TrimSpace(pattern) == "" {
			return fmt.Sprintf("%s.deny[%d]: пустой шаблон", name, i)
		}
	}
	for i, pattern := range p.Allow {
		if strings.TrimSpace(pattern) == "" {
			return fmt.Sprintf("%s.allow[%d]: пустой шаблон", name, i)
		}
	}
	return ""
}
