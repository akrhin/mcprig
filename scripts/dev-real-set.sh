#!/usr/bin/env bash
# Живой стенд на настоящих MCP-серверах: панель + открытый набор с serena
# (HTTP, LSP), socraticode (npx, индексация) и xerj (stdio, поиск).
#
#   bash scripts/dev-real-set.sh            # собрать и поднять на 9177 / 9101
#   PANEL_PORT=9197 SET_PORT=9102 bash scripts/dev-real-set.sh
#   bash scripts/dev-real-set.sh stop       # погасить панель и набор
#
# Данные — во временном каталоге, реестр твой не трогает.
set -euo pipefail

PANEL_PORT=${PANEL_PORT:-9177}
SET_PORT=${SET_PORT:-9101}
DATA=${DATA:-/tmp/mcprig-dev-real}
ROOT=$(cd "$(dirname "$0")/.." && pwd)
BIN="$DATA/mcprig"

if [ "${1:-}" = "stop" ]; then
	if [ -f "$DATA/panel.pid" ]; then
		kill "$(cat "$DATA/panel.pid")" 2>/dev/null || true
		rm -f "$DATA/panel.pid"
	fi
	echo "стенд погашен"
	exit 0
fi

mkdir -p "$DATA"
go build -o "$BIN" "$ROOT/cmd/mcprig"

# Повторный запуск: гасим прошлый стенд по pid-файлу, чтобы не занимать порты.
if [ -f "$DATA/panel.pid" ]; then
	kill "$(cat "$DATA/panel.pid")" 2>/dev/null || true
	rm -f "$DATA/panel.pid"
	sleep 0.5
fi

# setsid: стенд должен пережить завершение вызывающей оболочки.
MCPRIG_DATA_DIR="$DATA" MCPRIG_PORT="$PANEL_PORT" MCPRIG_AUTH=off \
	MCPRIG_CLI="$BIN" MCPRIG_BIND_IP=127.0.0.1 \
	setsid "$BIN" panel >"$DATA/panel.log" 2>&1 < /dev/null &
echo $! >"$DATA/panel.pid"
for _ in $(seq 1 100); do
	curl -sf -m 1 "http://127.0.0.1:$PANEL_PORT/healthz" >/dev/null && break
	sleep 0.1
done
# Порт мог быть занят чужой панелью: отвечает 200, а наш процесс уже умер.
# Без этой проверки POST'ы ушли бы в чужой реестр.
if ! kill -0 "$(cat "$DATA/panel.pid")" 2>/dev/null; then
	echo "панель не поднялась на $PANEL_PORT (порт занят?) — см. $DATA/panel.log" >&2
	exit 1
fi

api() { curl -s -X"$1" "http://127.0.0.1:$PANEL_PORT$2" -H 'Content-Type: application/json' ${3:+-d "$3"}; }
api POST /api/servers '{"name":"serena","url":"http://127.0.0.1:8899/mcp"}' >/dev/null
api POST /api/servers '{"name":"socraticode","command":["npx","-y","socraticode@1.16.1"]}' >/dev/null
api POST /api/servers "{\"name\":\"xerj\",\"command\":[\"$HOME/.local/bin/xerj\",\"mcp\"]}" >/dev/null
api POST /api/sets "{\"name\":\"real\",\"servers\":[\"serena\",\"socraticode\",\"xerj\"],\"open\":true,\"port\":$SET_PORT}" >/dev/null
api POST /api/sets/real/start '' >/dev/null

cat <<EOF
панель:  http://127.0.0.1:$PANEL_PORT   (auth off, данные $DATA)
набор:   http://127.0.0.1:$SET_PORT/mcp  (открытый, без ключа)
лог:     $DATA/sets/real/lazy-mcp.log

пример:
  curl -s -XPOST http://127.0.0.1:$SET_PORT/mcp \\
    -H 'Content-Type: application/json' -H 'Accept: application/json, text/event-stream' \\
    -d '{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"invoke_command","arguments":{"server":"serena","command_name":"activate_project","parameters":{"project":"$ROOT"}}}}'

погасить: bash scripts/dev-real-set.sh stop
EOF
