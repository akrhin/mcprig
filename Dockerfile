# mcprig in a container. Two targets:
#   slim — the binary, CA roots plus bubblewrap and prlimit: HTTP servers and
#          static stdio binaries run here, but there is no node/npm and no
#          uv/uvx, so those stdio servers cannot start.
#   full — plus node/npm, python/pip and uv/uvx, so npx/uvx-based stdio servers
#          run here too.
#
# Both images export MCPRIG_HOST=0.0.0.0 (the set listens on 9100, the panel on
# MCPRIG_PORT, default 9091). The sandbox still needs unprivileged user
# namespaces from the runtime: the images carry bwrap and prlimit, but the
# kernel/runtime has to allow bwrap.
#
# VERSION — версия релиза БЕЗ ведущего v (goreleaser отдаёт X.Y.Z); сборка из
# дерева оставляет dev. Форма одна: иначе main.version и метка образа разойдутся
# с тегом (v0.3.8 против 0.3.8), и это ловит проверка в стадии build.
#
#   docker build --build-arg VERSION=0.3.8 -t mcprig:slim .   # последняя стадия = slim
#   docker build --target full --build-arg VERSION=0.3.8 -t mcprig:full .
#
# Стадии идут от fat к thin: без --target Docker берёт последнюю, и по
# умолчанию должен получаться образ на 44 МБ, а не на 446.

FROM golang:1.27-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=dev
# Одна форма версии: vX.Y.Z отвергается, чтобы не просочиться в main.version.
RUN case "$VERSION" in v*) echo "VERSION должен быть без ведущего v, получено $VERSION" >&2; exit 1;; esac
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /out/mcprig ./cmd/mcprig

FROM node:22-alpine AS full
# REVISION — git-коммит, из которого собрано (метка image.revision).
ARG VERSION=dev
ARG REVISION=dev
LABEL org.opencontainers.image.title="mcprig (full)" \
      org.opencontainers.image.source="https://github.com/akrhin/mcprig" \
      org.opencontainers.image.version="${VERSION}" \
      org.opencontainers.image.revision="${REVISION}" \
      org.opencontainers.image.licenses="MIT"
RUN apk add --no-cache ca-certificates python3 py3-pip bubblewrap util-linux && adduser -D -u 10001 mcprig \
    && mkdir -p /data /config && chown 10001:10001 /data /config
COPY --from=build /out/mcprig /usr/local/bin/mcprig
# uv/uvx в /usr/bin; без них stdio-серверы через uvx не стартуют.
# Пин: pip3 ставит последнюю, и образ менялся бы без правок в дереве; 0.13.7 проверена.
RUN pip3 install --break-system-packages uv==0.13.7
ENV MCPRIG_HOST=0.0.0.0
USER 10001
WORKDIR /data
EXPOSE 9100 9091
# Набор на 9100, панель на MCPRIG_PORT (по умолчанию 9091); /healthz есть у обоих.
HEALTHCHECK --interval=30s --timeout=3s CMD wget -qO- http://127.0.0.1:9100/healthz || wget -qO- http://127.0.0.1:9091/healthz
ENTRYPOINT ["/usr/local/bin/mcprig"]
CMD ["serve", "--config", "/config/servers.json"]

FROM alpine:3.21 AS slim
ARG VERSION=dev
ARG REVISION=dev
LABEL org.opencontainers.image.title="mcprig (slim)" \
      org.opencontainers.image.source="https://github.com/akrhin/mcprig" \
      org.opencontainers.image.version="${VERSION}" \
      org.opencontainers.image.revision="${REVISION}" \
      org.opencontainers.image.licenses="MIT"
RUN apk add --no-cache ca-certificates bubblewrap util-linux && adduser -D -u 10001 mcprig \
    && mkdir -p /data /config && chown 10001:10001 /data /config
COPY --from=build /out/mcprig /usr/local/bin/mcprig
ENV MCPRIG_HOST=0.0.0.0
USER 10001
WORKDIR /data
EXPOSE 9100 9091
# Набор на 9100, панель на MCPRIG_PORT (по умолчанию 9091); /healthz есть у обоих.
HEALTHCHECK --interval=30s --timeout=3s CMD wget -qO- http://127.0.0.1:9100/healthz || wget -qO- http://127.0.0.1:9091/healthz
ENTRYPOINT ["/usr/local/bin/mcprig"]
CMD ["serve", "--config", "/config/servers.json"]
