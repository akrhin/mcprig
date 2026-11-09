#!/usr/bin/env bash
# Печатает раздел CHANGELOG.md для релиза: scripts/release-notes.sh v0.3.3.
# Пустой вывод означает, что раздела нет — вызывающий решает, что подставить
# (goreleaser и GitHub умеют сгенерировать заметки из коммитов).
set -euo pipefail
tag=${1:?использование: release-notes.sh vX.Y.Z}
ver=${tag#v}
awk -v v="$ver" '
  $0 ~ "^## \\[" v "\\]" { on = 1; next }
  on && /^## \[/ { exit }
  on' CHANGELOG.md
