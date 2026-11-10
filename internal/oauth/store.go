// Package oauth содержит клиентскую часть OAuth 2.0 для MCP-серверов,
// отвечающих 401/403: хранилище токенов, loopback-приёмник кода и фабрику
// обработчика поверх auth.AuthorizationCodeHandler из go-sdk.
package oauth

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync"

	"golang.org/x/oauth2"

	"github.com/akrhin/mcprig/internal/atomicfile"
)

// Key возвращает ключ хранилища для пары «имя сервера + URL»: первые 32 hex
// символа sha256 от name+"\x00"+url.
func Key(serverName, serverURL string) string {
	sum := sha256.Sum256([]byte(serverName + "\x00" + serverURL))
	return hex.EncodeToString(sum[:])[:32]
}

// Store хранит токены в одном JSON-файле map[string]*oauth2.Token.
type Store struct {
	mu          sync.Mutex
	dir         string
	path        string
	clientsPath string
	tokens      map[string]*oauth2.Token
	clients     map[string]ClientInfo
}

// ClientInfo — регистрационные данные клиента, без которых refresh после
// перезапуска невозможен: token endpoint известен только после DCR/discovery.
type ClientInfo struct {
	ClientID     string           `json:"client_id"`
	ClientSecret string           `json:"client_secret,omitempty"`
	TokenURL     string           `json:"token_url"`
	AuthStyle    oauth2.AuthStyle `json:"auth_style,omitempty"`
}

// OpenStore открывает (при необходимости создаёт) хранилище в каталоге dir.
// Каталог создаётся с правами 0700, файл токенов — <dir>/oauth.json (0600),
// регистрационные данные клиентов — <dir>/oauth-clients.json.
func OpenStore(dir string) (*Store, error) {
	if dir == "" {
		return nil, errors.New("oauth: не указан каталог хранилища токенов")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("oauth: создать каталог хранилища %s: %w", dir, err)
	}
	s := &Store{
		dir:         dir,
		path:        filepath.Join(dir, "oauth.json"),
		clientsPath: filepath.Join(dir, "oauth-clients.json"),
		tokens:      map[string]*oauth2.Token{},
		clients:     map[string]ClientInfo{},
	}
	if err := s.readJSON(s.path, &s.tokens, "токенов"); err != nil {
		return nil, err
	}
	if err := s.readJSON(s.clientsPath, &s.clients, "клиентов"); err != nil {
		return nil, err
	}
	if s.tokens == nil {
		s.tokens = map[string]*oauth2.Token{}
	}
	if s.clients == nil {
		s.clients = map[string]ClientInfo{}
	}
	return s, nil
}

// readJSON читает файл в v; отсутствие файла — не ошибка.
func (s *Store) readJSON(path string, v any, what string) error {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("oauth: прочитать файл %s %s: %w", what, path, err)
	}
	if len(data) == 0 {
		return nil
	}
	if err := json.Unmarshal(data, v); err != nil {
		return fmt.Errorf("oauth: повреждён файл %s %s: %w", what, path, err)
	}
	return nil
}

// Load возвращает сохранённый токен для key. Отдаётся копия: вызывающий не
// может испортить состояние хранилища.
func (s *Store) Load(key string) (*oauth2.Token, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	tok, ok := s.tokens[key]
	if !ok {
		return nil, false
	}
	cp := *tok
	return &cp, true
}

// Save сохраняет токен для key и сразу пишет файл. При ошибке записи
// состояние откатывается.
func (s *Store) Save(key string, tok *oauth2.Token) error {
	if tok == nil {
		return errors.New("oauth: нельзя сохранить пустой токен")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	prev, had := s.tokens[key]
	cp := *tok
	s.tokens[key] = &cp
	if err := s.writeAtomic(s.path, s.tokens); err != nil {
		if had {
			s.tokens[key] = prev
		} else {
			delete(s.tokens, key)
		}
		return err
	}
	return nil
}

// Delete удаляет токен для key и сразу пишет файл.
func (s *Store) Delete(key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	prev, had := s.tokens[key]
	if !had {
		return nil
	}
	delete(s.tokens, key)
	if err := s.writeAtomic(s.path, s.tokens); err != nil {
		s.tokens[key] = prev
		return err
	}
	return nil
}

// LoadClient возвращает регистрационные данные клиента для key.
func (s *Store) LoadClient(key string) (ClientInfo, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	info, ok := s.clients[key]
	return info, ok
}

// SaveClient запоминает client_id и token endpoint: без них обновление
// сохранённого токена после перезапуска невозможно.
func (s *Store) SaveClient(key string, info ClientInfo) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	prev, had := s.clients[key]
	if prev == info {
		return nil
	}
	s.clients[key] = info
	if err := s.writeAtomic(s.clientsPath, s.clients); err != nil {
		if had {
			s.clients[key] = prev
		} else {
			delete(s.clients, key)
		}
		return err
	}
	return nil
}

// writeAtomic пишет файл целиком атомарной заменой. Вызывать под s.mu.
func (s *Store) writeAtomic(path string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("oauth: сериализовать %s: %w", path, err)
	}
	if err := atomicfile.Write(path, data); err != nil {
		return fmt.Errorf("oauth: записать %s: %w", path, err)
	}
	return nil
}
