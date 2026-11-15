package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

// RateLimit bounds how often one caller may call a set's tools. It lives in the
// registry entry of the set and is copied into the set's own servers.json, so
// the proxy process enforces it where the calls happen.
//
// Нет поля или ноль — лимита нет: так набор ведёт себя и без блока.
type RateLimit struct {
	// PerMinute — общий лимит вызовов в минуту на пару (клиент, инструмент).
	PerMinute int `json:"perMinute,omitempty"`
	// Burst — сколько вызовов проходят одной пачкой; по умолчанию PerMinute.
	Burst int `json:"burst,omitempty"`
	// PerTool переопределяет лимит для пары "server/tool".
	PerTool map[string]RateLimit `json:"perTool,omitempty"`
}

// Decode разбирает блок из записи набора или из servers.json. Неизвестное поле
// — ошибка, а не молчаливый ноль: "perMunute" иначе снял бы лимит, о котором
// владелец думал, что он есть.
func (r *RateLimit) Decode(raw []byte) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	return dec.Decode(r)
}

// Check возвращает описание дефекта; пустая строка — блок годен.
func (r *RateLimit) Check() string {
	if r == nil {
		return ""
	}
	if r.PerMinute < 0 {
		return "rateLimit.perMinute: не может быть отрицательным"
	}
	if r.Burst < 0 {
		return "rateLimit.burst: не может быть отрицательным"
	}
	for name, t := range r.PerTool {
		if strings.TrimSpace(name) == "" {
			return "rateLimit.perTool: имя инструмента не может быть пустым"
		}
		if !strings.Contains(name, "/") {
			return fmt.Sprintf("rateLimit.perTool.%s: ожидается server/tool", name)
		}
		if t.PerMinute < 0 {
			return fmt.Sprintf("rateLimit.perTool.%s.perMinute: не может быть отрицательным", name)
		}
		if t.Burst < 0 {
			return fmt.Sprintf("rateLimit.perTool.%s.burst: не может быть отрицательным", name)
		}
		if len(t.PerTool) > 0 {
			return fmt.Sprintf("rateLimit.perTool.%s.perTool: вложенный perTool не поддерживается", name)
		}
	}
	return ""
}

// Empty reports whether the block sets no limit at all: ноль в общем поле и
// ни одного ненулевого переопределения.
func (r *RateLimit) Empty() bool {
	if r == nil {
		return true
	}
	if r.PerMinute > 0 {
		return false
	}
	for _, t := range r.PerTool {
		if t.PerMinute > 0 {
			return false
		}
	}
	return true
}
