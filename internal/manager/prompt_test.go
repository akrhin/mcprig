package manager

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/akrhin/mcprig/internal/registry"
	"github.com/akrhin/mcprig/internal/toollock"
)

// promptEnv — менеджер с набором из двух серверов; живой каталог подставляет
// тест: поднимать процессы ради текста промта незачем.
type promptEnv struct {
	*env
	id string
}

func newPromptEnv(t *testing.T) promptEnv {
	t.Helper()
	e := newEnv(t, AuthOptions{Off: true})
	e.addServer(t, "alpha")
	e.addServer(t, "beta")
	w := e.do(t, http.MethodPost, "/api/sets", map[string]any{
		"name": "p", "servers": []string{"alpha", "beta"},
	})
	status(t, w, http.StatusCreated)
	created := decode[struct {
		ID string `json:"id"`
	}](t, w)
	return promptEnv{env: e, id: created.ID}
}

func (p promptEnv) prompt(t *testing.T, body map[string]any) (string, []string) {
	t.Helper()
	w := p.do(t, http.MethodPost, "/api/sets/"+p.id+"/prompt", body)
	status(t, w, http.StatusOK)
	got := decode[struct {
		Prompt string   `json:"prompt"`
		Notes  []string `json:"notes"`
	}](t, w)
	return got.Prompt, got.Notes
}

// catalogOf подставляет живой каталог с одним инструментом на сервер.
func (p promptEnv) catalogOf(task func(name string) promptTool) {
	p.m.running = func(*registry.Set) bool { return true }
	p.m.catalog = func(_ context.Context, _ *registry.Set, names []string) promptCatalog {
		cat := emptyCatalog(names)
		for i := range cat.Servers {
			cat.Servers[i].Tools = []promptTool{task(cat.Servers[i].Name)}
		}
		return cat
	}
}

// Три части идут в объявленном порядке: вводная, перечень, задача — и задача
// дословно последней.
func TestSetPromptPartsOrder(t *testing.T) {
	p := newPromptEnv(t)
	schema := `{"type":"object","properties":{"x":{"type":"string"}}}`
	p.catalogOf(func(name string) promptTool {
		return promptTool{Name: "run_" + name, Description: "делает дело", Schema: schema}
	})

	prompt, notes := p.prompt(t, map[string]any{"task": "найди ошибку в логе", "intro": "ты — отладчик"})
	iIntro := strings.Index(prompt, "ты — отладчик")
	iTools := strings.Index(prompt, promptToolsHeading)
	iTask := strings.Index(prompt, promptTaskHeading)
	if iIntro < 0 || iTools < 0 || iTask < 0 || !(iIntro < iTools && iTools < iTask) {
		t.Fatalf("порядок частей нарушен: intro=%d tools=%d task=%d\n%s", iIntro, iTools, iTask, prompt)
	}
	if !strings.HasSuffix(prompt, "найди ошибку в логе\n") {
		t.Fatalf("задача не последняя и не дословная:\n%s", prompt)
	}
	for _, want := range []string{"run_alpha", "run_beta", schema} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("в промте нет %q:\n%s", want, prompt)
		}
	}
	if len(notes) != 0 {
		t.Fatalf("заметки без причины: %v", notes)
	}
}

// Пустая вводная — короткий шаблон, а не пустая строка.
func TestSetPromptDefaultIntro(t *testing.T) {
	p := newPromptEnv(t)
	p.catalogOf(func(name string) promptTool { return promptTool{Name: "t_" + name} })

	prompt, _ := p.prompt(t, map[string]any{"task": "задача"})
	if !strings.HasPrefix(prompt, "Ты работаешь с MCP-набором "+p.id) {
		t.Fatalf("нет шаблона вводной:\n%s", prompt)
	}
}

// Фильтр servers оставляет только перечисленные серверы.
func TestSetPromptServersFilter(t *testing.T) {
	p := newPromptEnv(t)
	p.catalogOf(func(name string) promptTool { return promptTool{Name: "run_" + name} })

	prompt, _ := p.prompt(t, map[string]any{"task": "задача", "servers": []string{"beta"}})
	if !strings.Contains(prompt, "run_beta") || strings.Contains(prompt, "run_alpha") {
		t.Fatalf("фильтр не сработал:\n%s", prompt)
	}
}

// Имя сервера не из набора — отказ, а не молчаливое сужение: иначе в промт
// попали бы инструменты чужого набора.
func TestSetPromptRejectsForeignServer(t *testing.T) {
	p := newPromptEnv(t)
	p.catalogOf(func(name string) promptTool { return promptTool{Name: "run_" + name} })

	w := p.do(t, http.MethodPost, "/api/sets/"+p.id+"/prompt", map[string]any{
		"task": "задача", "servers": []string{"чужой"},
	})
	status(t, w, http.StatusBadRequest)
	if body := w.Body.String(); !strings.Contains(body, "не входит") {
		t.Fatalf("отказ не назвал причину: %s", body)
	}
}

// Схема длиннее потолка обрезается с многоточием, и это видно в заметках.
func TestSetPromptTrimsSchema(t *testing.T) {
	p := newPromptEnv(t)
	big := `{"type":"object","properties":{"a":"` + strings.Repeat("x", 9000) + `"}}`
	p.catalogOf(func(name string) promptTool {
		return promptTool{Name: "run_" + name, Schema: big}
	})

	prompt, notes := p.prompt(t, map[string]any{"task": "задача"})
	var schema string
	for _, line := range strings.Split(prompt, "\n") {
		if strings.Contains(line, `"properties"`) {
			schema = strings.TrimSpace(line)
		}
	}
	if len(schema) > promptSchemaLimit || !strings.HasSuffix(schema, "…") {
		t.Fatalf("схема не обрезана (%d байт): %q", len(schema), schema)
	}
	joined := strings.Join(notes, "\n")
	if !strings.Contains(joined, "обрезана") {
		t.Fatalf("нет заметки об обрезке: %v", notes)
	}
	if len(prompt) > promptSizeLimit {
		t.Fatalf("промт длиннее потолка: %d", len(prompt))
	}
}

// Остановленный набор: имена из tools.lock.json, схем нет — и заметка об этом.
func TestSetPromptStoppedSet(t *testing.T) {
	p := newPromptEnv(t)
	path := toollock.Path(p.m.rt.dataDir, p.id)
	err := toollock.Update(path, func(f *toollock.File) bool {
		f.Tools = map[string]toollock.Entry{
			toollock.Key("alpha", "alpha_one"): {Hash: "h1"},
			toollock.Key("beta", "beta_one"):   {Hash: "h2"},
		}
		return true
	})
	if err != nil {
		t.Fatalf("tools.lock.json: %v", err)
	}

	prompt, notes := p.prompt(t, map[string]any{"task": "задача"})
	if !strings.Contains(prompt, "alpha_one") || !strings.Contains(prompt, "beta_one") {
		t.Fatalf("имён из tools.lock.json нет:\n%s", prompt)
	}
	if !strings.Contains(strings.Join(notes, "\n"), "набор не запущен, схемы недоступны") {
		t.Fatalf("нет заметки про схемы: %v", notes)
	}
}

// Токен и секреты серверов не попадают в промт ни из одного источника:
// ни из заметок, ни из описания и схемы инструмента.
func TestSetPromptNoToken(t *testing.T) {
	const literal = "literal-env-secret-9f3a"
	e := newEnv(t, AuthOptions{Off: true})
	e.addServerWithEnv(t, "alpha", map[string]string{"API_KEY": literal})
	e.addServer(t, "beta")
	p := promptEnv{env: e, id: e.addSet(t, "p", "alpha", "beta")}

	token := p.m.store.Set(p.id).Token
	if token == "" {
		t.Fatal("закрытый набор без токена")
	}
	schema := `{"type":"object","properties":{"token":{"type":"string","default":"` + token + `"}}}`
	p.m.running = func(*registry.Set) bool { return true }
	p.m.catalog = func(_ context.Context, _ *registry.Set, names []string) promptCatalog {
		cat := emptyCatalog(names)
		cat.Notes = append(cat.Notes, "каталог набора недоступен: bearer "+token)
		for i := range cat.Servers {
			cat.Servers[i].Tools = []promptTool{{
				Name:        "run_" + cat.Servers[i].Name,
				Description: "ходит в API с ключом " + literal,
				Schema:      schema,
			}}
		}
		return cat
	}

	prompt, notes := p.prompt(t, map[string]any{"task": "задача"})
	text := prompt + strings.Join(notes, "\n")
	for name, secret := range map[string]string{"токен набора": token, "литеральный env-секрет": literal} {
		if strings.Contains(text, secret) {
			t.Fatalf("%s утёк в промт:\n%s", name, text)
		}
	}
	if !strings.Contains(strings.Join(notes, "\n"), "***") {
		t.Fatalf("текст ошибки не замаскирован: %v", notes)
	}
	if !strings.Contains(prompt, "***") {
		t.Fatalf("описание или схема не замаскированы:\n%s", prompt)
	}
	// Маска не должна ломать схему: это по-прежнему JSON с той же структурой.
	var schemas []string
	for _, line := range strings.Split(prompt, "\n") {
		if line = strings.TrimSpace(line); strings.HasPrefix(line, `{"type":"object"`) {
			schemas = append(schemas, line)
		}
	}
	if len(schemas) != 2 {
		t.Fatalf("схем в промте %d, ожидалось 2:\n%s", len(schemas), prompt)
	}
	for _, raw := range schemas {
		var got struct {
			Properties map[string]any `json:"properties"`
		}
		if err := json.Unmarshal([]byte(raw), &got); err != nil {
			t.Fatalf("схема перестала быть JSON после маски: %v\n%s", err, raw)
		}
		if _, ok := got.Properties["token"]; !ok {
			t.Fatalf("структура схемы сломана: %s", raw)
		}
	}
	for _, want := range []string{"run_alpha", "run_beta"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("имя инструмента %q потерялось:\n%s", want, prompt)
		}
	}
}

// Пустая задача — 400, а не промт без задачи.
func TestSetPromptEmptyTask(t *testing.T) {
	p := newPromptEnv(t)
	w := p.do(t, http.MethodPost, "/api/sets/"+p.id+"/prompt", map[string]any{"task": "  "})
	status(t, w, http.StatusBadRequest)
	if got := decode[map[string]string](t, w)["error"]; got != "задача не задана" {
		t.Fatalf("ошибка = %q", got)
	}
}
