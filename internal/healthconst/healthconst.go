// Package healthconst держит числа здоровья, общие для панели (internal/manager)
// и прокси (internal/mcpserver): один источник, иначе они расходятся в том,
// кто «здоров».
package healthconst

import "time"

const (
	// TTL keeps one probe result (including a failure) before re-checking.
	TTL = 15 * time.Second
	// Timeout bounds one server's status probe. Холодный stdio (npx, uvx) и
	// первый коннект к LSP поднимаются дольше 15 с: меньший бюджет выглядит
	// мёртвым сервером, хотя вызовы проходят.
	Timeout = 15 * time.Second
)
