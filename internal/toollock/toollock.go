// Package toollock keeps the per-set record of approved tool descriptors.
//
// Карантин живёт файлом, а не памятью: одобрение переживает перезапуск
// набора, а пишут его два процесса — прокси набора (замечает изменение) и
// панель (одобряет). Формат знает только этот пакет.
package toollock

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/akrhin/mcprig/internal/atomicfile"
)

// Entry — одна запись об инструменте. Hash — одобренный дескриптор; Seen —
// хэш текущего, когда он разошёлся с одобренным. Seen заполняет процесс
// набора: только он видит дескрипторы, и панель одобряет ровно то, что он
// видел, — хэш не считается в двух местах.
type Entry struct {
	Hash       string `json:"hash"`
	Seen       string `json:"seen,omitempty"`
	ApprovedAt string `json:"approvedAt"`
	ApprovedBy string `json:"approvedBy"`
}

// File — содержимое tools.lock.json; ключ записи — "сервер/инструмент".
type File struct {
	Tools map[string]Entry `json:"tools"`
}

// Path — <dataDir>/sets/<id>/tools.lock.json, рядом с servers.json набора.
func Path(dataDir, setID string) string {
	return filepath.Join(dataDir, "sets", setID, "tools.lock.json")
}

// Key собирает ключ записи. Имена серверов и инструментов не содержат "/",
// поэтому Split возвращает их обратно без словаря.
func Key(server, tool string) string { return server + "/" + tool }

// Split разбирает ключ записи.
func Split(key string) (server, tool string) {
	if i := strings.IndexByte(key, '/'); i >= 0 {
		return key[:i], key[i+1:]
	}
	return key, ""
}

// Read читает записи. Отсутствующий или битый файл — пустой список: первое
// доверие пишется при первом появлении инструмента, и сломанный файл не
// должен запирать набор целиком. Битый файл не молчит: потерянные одобрения
// иначе выглядят как «карантин сам снялся».
//
// shortcut: битый файл теряет прежние одобрения (инструменты одобрятся
// заново как новые); перейти на резервную копию, если это начнёт мешать.
func Read(path string) File {
	f, err := ReadFile(path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		slog.Warn("tools.lock.json не прочитан — одобрения неизвестны", "path", path, "err", err)
	}
	return f
}

// ReadFile — Read с ошибкой: карантин обязан отличить «записей нет»
// (fs.ErrNotExist — обычный старт набора) от «записи не прочитаны»: во втором
// случае решение брать неоткуда, и вызов fail-closed отклоняется.
func ReadFile(path string) (File, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return File{Tools: map[string]Entry{}}, err
	}
	var f File
	if err := json.Unmarshal(raw, &f); err != nil {
		return File{Tools: map[string]Entry{}}, fmt.Errorf("tools.lock.json не разобран: %w", err)
	}
	if f.Tools == nil {
		f.Tools = map[string]Entry{}
	}
	return f, nil
}

// Update — единственный писатель файла: читает под межпроцессной блокировкой,
// отдаёт правку и пишет, если fn сказал, что изменил. Панель и процесс набора
// пишут один файл из разных процессов, и без блокировки поздняя запись
// затирала чужое одобрение.
//
// Битый файл — ошибка, а не пустой список: иначе все дескрипторы одобрились бы
// заново как новые, и карантин снялся бы сам. Отсутствующий файл — обычный
// старт набора, правка применяется.
func Update(path string, fn func(f *File) bool) error {
	unlock, err := lock(path + ".lock")
	if err != nil {
		return err
	}
	defer unlock()
	f, err := ReadFile(path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	if !fn(&f) {
		return nil
	}
	return Write(path, f)
}

// lock берёт эксклюзивную flock на отдельном файле: сам tools.lock.json
// подменяется rename-ом, и блокировать его inode бессмысленно.
func lock(path string) (func(), error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	fh, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(fh.Fd()), syscall.LOCK_EX); err != nil {
		_ = fh.Close() // первична ошибка flock
		return nil, err
	}
	return func() {
		_ = syscall.Flock(int(fh.Fd()), syscall.LOCK_UN) // снимется и закрытием файла
		_ = fh.Close()
	}, nil
}

// Write заменяет файл целиком: читатель не видит половину записи.
func Write(path string, f File) error {
	if f.Tools == nil {
		f.Tools = map[string]Entry{}
	}
	raw, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	return atomicfile.Write(path, append(raw, '\n'))
}

// Approve делает текущий дескриптор одобренным. false — одобрять нечего:
// запись не менялась или её нет.
func Approve(e Entry, by, at string) (Entry, bool) {
	if e.Seen == "" || e.Seen == e.Hash {
		return e, false
	}
	e.Hash, e.Seen, e.ApprovedAt, e.ApprovedBy = e.Seen, "", at, by
	return e, true
}

// Hash — sha256 канонической записи дескриптора. Порядок ключей задаёт
// json.Marshal структуры, а не байты ответа: апстрим вправе переставить ключи
// схемы, не меняя её смысла.
func Hash(t *mcp.Tool) string {
	canon := struct {
		Name         string               `json:"name"`
		Description  string               `json:"description"`
		InputSchema  any                  `json:"inputSchema"`
		OutputSchema any                  `json:"outputSchema"`
		Annotations  *mcp.ToolAnnotations `json:"annotations"`
	}{
		Name:         t.Name,
		Description:  t.Description,
		InputSchema:  t.InputSchema,
		OutputSchema: t.OutputSchema,
		Annotations:  t.Annotations,
	}
	raw, err := json.Marshal(canon)
	if err != nil {
		// Схемы уже пришли из JSON: кодирование их же не падает.
		return ""
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

// Message — текст отказа. Он один на все пути: invoke_command, invoke_many и
// кнопка browse называют человеку ту же команду, называя оба хэша.
func Message(server, tool, approved, now, setID string) string {
	command := "mcprig approve " + Key(server, tool)
	if setID != "" {
		command = "mcprig approve --set " + setID + " " + Key(server, tool)
	}
	return fmt.Sprintf("🚫 Инструмент изменился: %s/%s (одобрен %s, сейчас %s); одобрите: %s",
		server, tool, Short(approved), Short(now), command)
}

// Short — первые 12 знаков хэша: хватает, чтобы человек сравнил записи в
// панели, и не занимает пол-строки двумя полными хэшами.
func Short(hash string) string {
	if len(hash) > 12 {
		return hash[:12]
	}
	return hash
}
