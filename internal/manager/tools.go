package manager

// Инструменты набора и их карантин. Файл tools.lock.json пишет процесс набора:
// только он видит дескрипторы. Панель его читает и одобряет текущий хэш —
// после этого вызов снова проходит.

import (
	"net/http"
	"sort"
	"time"

	"github.com/akrhin/mcprig/internal/toollock"
)

type toolView struct {
	Server string `json:"server"`
	Tool   string `json:"tool"`
	// Hash — текущий дескриптор, ApprovedHash — одобренный: у changed они
	// расходятся, и одобрение снимает расхождение.
	Hash         string `json:"hash"`
	ApprovedHash string `json:"approvedHash"`
	State        string `json:"state"`
}

func (m *Manager) apiSetTools(req *request) error {
	id := req.args[0]
	if m.store.Set(id) == nil {
		return notFound("набор " + id + " не найден")
	}
	lock := toollock.Read(toollock.Path(m.rt.dataDir, id))
	out := make([]toolView, 0, len(lock.Tools))
	for key, entry := range lock.Tools {
		server, tool := toollock.Split(key)
		view := toolView{Server: server, Tool: tool, Hash: entry.Hash, ApprovedHash: entry.Hash, State: "ok"}
		if entry.Seen != "" && entry.Seen != entry.Hash {
			view.Hash = entry.Seen
			view.State = "changed"
		}
		out = append(out, view)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Server != out[j].Server {
			return out[i].Server < out[j].Server
		}
		return out[i].Tool < out[j].Tool
	})
	writeJSON(req.w, http.StatusOK, map[string]any{"tools": out})
	return nil
}

// apiSetToolsApprove одобряет текущий дескриптор: {server, tool} для одного
// инструмента или {all: true} для всех изменившихся набора.
func (m *Manager) apiSetToolsApprove(req *request) error {
	id := req.args[0]
	if m.store.Set(id) == nil {
		return notFound("набор " + id + " не найден")
	}
	var in struct {
		Server string `json:"server"`
		Tool   string `json:"tool"`
		All    bool   `json:"all"`
	}
	if err := decodeJSON(req.r, &in); err != nil {
		return badRequest("тело запроса: " + err.Error())
	}
	if !in.All && (in.Server == "" || in.Tool == "") {
		return badRequest("нужны server и tool или all: true")
	}

	path := toollock.Path(m.rt.dataDir, id)
	now := time.Now().UTC().Format(time.RFC3339)
	approved := make([]string, 0, 8)
	// Автор записи — всегда панель: CLI одобряет тем же маршрутом, и поле by
	// в теле не принимается, иначе одобрение выдаёт себя за чужое.
	err := toollock.Update(path, func(lock *toollock.File) bool {
		dirty := false
		for key, entry := range lock.Tools {
			server, tool := toollock.Split(key)
			if !in.All && (server != in.Server || tool != in.Tool) {
				continue
			}
			approvedEntry, ok := toollock.Approve(entry, "panel", now)
			if !ok {
				continue
			}
			lock.Tools[key] = approvedEntry
			approved = append(approved, key)
			dirty = true
		}
		return dirty
	})
	if err != nil {
		return serverError("запись " + path + ": " + err.Error())
	}
	if !in.All && len(approved) == 0 {
		return conflict("нечего одобрять: " + in.Server + "/" + in.Tool + " не менялся или неизвестен")
	}
	sort.Strings(approved)
	// Ответ — фактическое состояние файла после записи: процесс набора мог
	// успеть заметить новое изменение, и тогда «одобрено» было бы неправдой.
	if len(approved) > 0 {
		final := toollock.Read(path).Tools
		kept := approved[:0]
		for _, key := range approved {
			if cur, ok := final[key]; ok && cur.Seen == "" {
				kept = append(kept, key)
			}
		}
		approved = kept
	}
	writeJSON(req.w, http.StatusOK, map[string]any{"approved": approved})
	return nil
}
