#!/usr/bin/env bash
# Печатает раздел CHANGELOG.md для релиза: scripts/release-notes.sh v0.3.4.
# Пустой раздел — ошибка: иначе релиз уходит без заметок, как v0.3.4, а
# goreleaser и gh молча подставляют сгенерированные «Full Changelog».
set -euo pipefail
tag=${1:?использование: release-notes.sh vX.Y.Z}
ver=${tag#v}
notes=$(awk -v v="$ver" '
  $0 ~ "^## \\[" v "\\]" { on = 1; next }
  on && /^## \[/ { exit }
  on' CHANGELOG.md)
if [ -z "${notes//[[:space:]]/}" ]; then
  echo "release-notes: в CHANGELOG.md нет раздела ## [$ver] — перед тегом перенеси Unreleased под номер версии" >&2
  exit 1
fi
printf '%s\n' "$notes"
