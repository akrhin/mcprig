# mcprig in a container. Two targets:
#   slim — only the binary and CA roots; downstream servers must be HTTP.
#   full — plus node and python, so npx/uvx-based stdio servers run here too.
#
#   docker build -t mcprig:slim .                 # последняя стадия = slim
#   docker build --target full -t mcprig:full .
#
# Стадии идут от fat к thin: без --target Docker берёт последнюю, и по
# умолчанию должен получаться образ на 20 МБ, а не на 330.

FROM golang:1.27-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /out/mcprig ./cmd/mcprig

FROM node:22-alpine AS full
RUN apk add --no-cache ca-certificates python3 py3-pip && adduser -D -u 10001 mcprig \
    && mkdir -p /data /config && chown 10001:10001 /data /config
COPY --from=build /out/mcprig /usr/local/bin/mcprig
USER 10001
WORKDIR /data
EXPOSE 9100
# Порт берётся из transport.port конфига; по умолчанию 9100.
HEALTHCHECK --interval=30s --timeout=3s CMD wget -qO- http://127.0.0.1:9100/healthz >/dev/null || exit 1
ENTRYPOINT ["/usr/local/bin/mcprig"]
CMD ["serve", "--config", "/config/servers.json"]

FROM alpine:3.21 AS slim
RUN apk add --no-cache ca-certificates && adduser -D -u 10001 mcprig \
    && mkdir -p /data /config && chown 10001:10001 /data /config
COPY --from=build /out/mcprig /usr/local/bin/mcprig
USER 10001
WORKDIR /data
EXPOSE 9100
# Порт берётся из transport.port конфига; по умолчанию 9100.
HEALTHCHECK --interval=30s --timeout=3s CMD wget -qO- http://127.0.0.1:9100/healthz >/dev/null || exit 1
ENTRYPOINT ["/usr/local/bin/mcprig"]
CMD ["serve", "--config", "/config/servers.json"]
