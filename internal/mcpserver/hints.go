package mcpserver

import "github.com/akrhin/mcprig/internal/config"

// serverHints — одна строка «для чего этот сервер и с чего начать». Без неё
// агент видит только имя и описание и выбирает сервер наугад: у serena,
// socraticode и xerj пересекается всё — поиск, — а решают они разные задачи.
// Таблица держится одним файлом: правка подсказки не должна ждать релиза
// сервера, а знание о нём есть только здесь. Только устойчивое: что сервер
// решает и с чего начать. Доступность и сравнение серверов протухают — им тут
// не место.
//
//nolint:gosec // G101: «pw» он находит в имени deepwiki — это подсказки, а не секреты.
var serverHints = map[string]string{
	"serena":            "Символы и ссылки в Go/TS: сначала initial_instructions, затем activate_project с абсолютным путём.",
	"socraticode":       "Смысловой поиск по проиндексированному проекту; в worktree сначала codebase_index.",
	"xerj":              "PDF, логи, почта, CSV и архив сессий; index (или prefix) обязателен в каждом поиске.",
	"context7":          "Документация библиотек; пин версии в libraryId замедляет ответ до таймаута.",
	"deepwiki":          "«Как устроен чужой репозиторий»: публичные репозитории, ответ за секунды.",
	"playwright":        "Браузер: навигация, сеть, консоль, evaluate; один браузер на сервер — при занятости ждать.",
	"gitea":             "Issues, PR и Actions; в list_issues нужен type:\"issues\", иначе придут pull request'ы.",
	"portainer-mcp":     "Docker на удалённых хостах; environment обязателен в каждом вызове.",
	"arcane-mcp":        "Контейнеры и CVE через Arcane; параметры идут путём /agents/{id}/….",
	"dap-mcp":           "Отладка по брейкпоинтам: breakpoints ставятся до старта сессии, threadId обязателен.",
	"raven":             "Аудит UI по реальному рендеру, 30–40 с на страницу.",
	"designer-skill":    "Гейты дизайна (anti-slop, контраст); части тулов нужны MCP roots.",
	"tavily-mcp":        "Веб-поиск и извлечение текста страницы; пустой Content — битый URL.",
	"open-design":       "Макеты и их прогоны.",
	"code-review-graph": "Граф изменений репозитория; repo_root обязателен, сам граф строит CLI.",
}

// serverHint — подсказка сервера: своя из конфига набора важнее встроенной.
// Пустая строка — сервер незнаком, поля hint в ответе не будет.
func serverHint(srv *config.Server) string {
	if srv.Hint != "" {
		return srv.Hint
	}
	return serverHints[srv.Name]
}
