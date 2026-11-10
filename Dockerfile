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
#   docker build -t mcprig:slim .                 # последняя стадия = slim
#   docker build --target full -t mcprig:full .
#
# Стадии идут от fat к thin: без --target Docker берёт последнюю, и по
# умолчанию должен получаться образ на 44 МБ, а не на 446.

FROM golang:1.27-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /out/mcprig ./cmd/mcprig

FROM node:22-alpine AS full
RUN apk add --no-cache ca-certificates python3 py3-pip bubblewrap util-linux && adduser -D -u 10001 mcprig \
    && mkdir -p /data /config && chown 10001:10001 /data /config
COPY --from=build /out/mcprig /usr/local/bin/mcprig
# uv/uvx в /usr/bin; без них stdio-серверы через uvx не стартуют.
RUN pip3 install --break-system-packages uv
ENV MCPRIG_HOST=0.0.0.0
USER 10001
WORKDIR /data
EXPOSE 9100 9091
# Порт берётся из transport.port конфига; по умолчанию 9100.
HEALTHCHECK --interval=30s --timeout=3s CMD wget -qO- http://127.0.0.1:9100/healthz >/dev/null || exit 1
ENTRYPOINT ["/usr/local/bin/mcprig"]
CMD ["serve", "--config", "/config/servers.json"]

FROM alpine:3.21 AS slim
RUN apk add --no-cache ca-certificates bubblewrap util-linux && adduser -D -u 10001 mcprig \
    && mkdir -p /data /config && chown 10001:10001 /data /config
COPY --from=build /out/mcprig /usr/local/bin/mcprig
ENV MCPRIG_HOST=0.0.0.0
USER 10001
WORKDIR /data
EXPOSE 9100 9091
# Порт берётся из transport.port конфига; по умолчанию 9100.
HEALTHCHECK --interval=30s --timeout=3s CMD wget -qO- http://127.0.0.1:9100/healthz >/dev/null || exit 1
ENTRYPOINT ["/usr/local/bin/mcprig"]
CMD ["serve", "--config", "/config/servers.json"]
