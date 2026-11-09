#!/usr/bin/env bash
# sandbox-e2e.sh — проверяет, что bwrap-песочница действительно держит stdio
# сервер: ни $HOME, ни секретов, ни сети, ни записи, ни /proc/1/root.
#
# argv ниже повторяет internal/sandbox/sandbox.go (те же флаги и порядок).
# Расхождение ловится юнит-тестом TestWrapBwrapArgv; здесь проверяется
# поведение хоста: сам bwrap, /proc, mount, unix-сокеты, лимиты.
#
# Env-whitelist (downstream.BaseEnv) проверяется Go-тестом
# TestSandboxIsolatesStdioServer (Canary:clean) — здесь он не воспроизводим:
# bwrap наследует env, сужение делает Go.
#
# Запуск: bash scripts/sandbox-e2e.sh   (переменные: MEMORY_MB, PIDS)
set -uo pipefail

BWRAP=${BWRAP:-/usr/bin/bwrap}
MEMORY_MB=${MEMORY_MB:-4096}
PIDS=${PIDS:-256}
TIMEOUT_SEC=${TIMEOUT_SEC:-20}
ROOT=$(cd "$(dirname "$0")/.." && pwd)
WORK=$(mktemp -d "${TMPDIR:-/tmp}/mcprig-sandbox-e2e.XXXXXX")
FAILED=0
SOCK="$WORK/host.sock"
trap 'rm -rf "$WORK"' EXIT

pass() { printf 'PASS  %s\n' "$1"; }
fail() { printf 'FAIL  %s — %s\n' "$1" "$2"; FAILED=$((FAILED + 1)); }

if [ ! -x "$BWRAP" ]; then
	echo "FAIL  bwrap не найден в $BWRAP" >&2
	exit 1
fi
if ! "$BWRAP" --unshare-all --die-with-parent --ro-bind-try /usr /usr --ro-bind-try /lib /lib \
	--ro-bind-try /lib64 /lib64 -- /usr/bin/true 2>/dev/null; then
	echo "FAIL  bwrap не запускается (userns выключен?)" >&2
	exit 1
fi

mkdir -p "$WORK/rw"

ARGS=("$BWRAP" --tmpfs /tmp --proc /proc --dev /dev --bind "$WORK/rw" "$WORK/rw")
for p in /usr /bin /lib /lib64 /etc/ssl /etc/resolv.conf /etc/passwd /etc/group; do
	[ -e "$p" ] && ARGS+=(--ro-bind "$p" "$p")
done
ARGS+=(--remount-ro / --unshare-user --unshare-pid --unshare-ipc --unshare-uts
	--unshare-cgroup-try --unshare-net --die-with-parent --new-session
	--chdir / --setenv TMPDIR /tmp -- prlimit "--as=$((MEMORY_MB * 1024 * 1024))" "--nproc=$PIDS" --)

# sb <команда...> — запускает команду в песочнице.
sb() {
	timeout "$TIMEOUT_SEC" env -i PATH=/usr/bin:/bin LANG=C TZ=UTC TMPDIR=/tmp "${ARGS[@]}" "$@"
}

# 1. Домашний каталог и ключ: их в песочнице нет.
if sb /usr/bin/test -e "$HOME" 2>/dev/null; then
	fail "$HOME виден" "stat прошёл"
else
	pass "$HOME не виден"
fi
if sb /usr/bin/cat "$HOME/.ssh/id_ed25519" >/dev/null 2>&1; then
	fail "~/.ssh/id_ed25519 читается" "cat прошёл"
else
	pass "~/.ssh/id_ed25519 не читается"
fi

# 2. registry.json лежит рядом с состоянием владельца — тоже недоступен.
REG="${XDG_STATE_HOME:-$HOME/.local/state}/mcprig/registry.json"
if sb /usr/bin/cat "$REG" >/dev/null 2>&1; then
	fail "registry.json читается" "cat прошёл"
else
	pass "registry.json не читается"
fi

# 3. Сети нет: ни DNS, ни TCP.
if sb /usr/bin/timeout 5 /usr/bin/getent hosts example.com >/dev/null 2>&1; then
	fail "DNS работает" "getent вернул адрес"
else
	pass "DNS недоступен"
fi
if [ -x /usr/bin/curl ]; then
	if sb /usr/bin/timeout 5 /usr/bin/curl -sS -m 4 http://1.1.1.1 >/dev/null 2>&1; then
		fail "curl наружу прошёл" "HTTP-ответ получен"
	else
		pass "curl наружу не прошёл"
	fi
fi

# 4. Запись: только в привязанный rw-каталог.
if sb /usr/bin/touch /pwned 2>/dev/null; then
	fail "запись в / прошла" "touch /pwned"
else
	pass "запись в / отклонена"
fi
if sb /usr/bin/touch /etc/pwned 2>/dev/null; then
	fail "запись в /etc прошла" "touch /etc/pwned"
else
	pass "запись в /etc отклонена"
fi
if sb /usr/bin/touch "$WORK/rw/ok" 2>/dev/null; then
	pass "в rw-каталог писать можно"
else
	fail "в rw-каталог не пишется" "touch в --bind"
fi

# 5. env: реальный Go-путь (whitelist + envAllow) проверяется только через
# mcprig: bwrap env наследует, сужение делает downstream.BaseEnv. Гоняем
# интеграционный тест — он и есть эта проверка, а не его дубль.
if go test -C "$ROOT" ./internal/sandbox -run TestSandboxIsolatesStdioServer -count=1 >/dev/null 2>&1; then
	pass "env: канарейка хоста не доходит до сервера (Canary:clean)"
else
	fail "env: канарейка" "TestSandboxIsolatesStdioServer не прошёл"
fi

# 6. /proc/1/root — корень песочницы, а не хоста.
if sb /usr/bin/cat /proc/1/root/etc/shadow >/dev/null 2>&1; then
	fail "/proc/1/root/etc/shadow читается" "cat прошёл"
else
	pass "/proc/1/root не выводит на хост"
fi

# 7. mount внутри — отказ: нет CAP_SYS_ADMIN.
if sb /usr/bin/mount -t tmpfs none /mnt 2>/dev/null; then
	fail "mount прошёл" "tmpfs примонтирован"
else
	pass "mount отклонён"
fi

# 8. Unix-сокет хоста не виден: /tmp внутри — свой tmpfs.
if /usr/bin/python3 -c 'import socket,sys; s=socket.socket(socket.AF_UNIX); s.bind(sys.argv[1])' "$SOCK" 2>/dev/null; then
	if sb /usr/bin/test -S "$SOCK" 2>/dev/null; then
		fail "unix-сокет хоста виден" "$SOCK"
	else
		pass "unix-сокет хоста не виден"
	fi
	rm -f "$SOCK"
else
	fail "unix-сокет хоста" "python3 не создал сокет — проверить нечем"
fi

# 9. Лимит nproc: 600 фоновых процессов не поднимаются, хостовый шелл жив.
out=$(sb /usr/bin/bash -c 'for i in $(seq 600); do sleep 30 & done; wait' 2>&1)
if echo "$out" | /usr/bin/grep -qiE 'Resource temporarily unavailable|Cannot fork|fork'; then
	pass "nproc-лимит обрывает потомков"
else
	fail "nproc-лимит не сработал" "$(echo "$out" | head -1)"
fi

# 10. Лимит памяти: 4 ГиБ не выделяются при --as.
if sb /usr/bin/python3 -c 'x=bytearray(4*1024**3)' >/dev/null 2>&1; then
	fail "выделено 4 ГиБ" "MemoryError не пришёл"
else
	pass "лимит памяти срабатывает"
fi

echo
if [ "$FAILED" -ne 0 ]; then
	echo "ИТОГ: провалено проверок — $FAILED"
	exit 1
fi
echo "ИТОГ: все проверки прошли"
