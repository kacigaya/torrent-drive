FROM oven/bun:1.4.4 AS frontend
WORKDIR /src/frontend
ENV NEXT_TELEMETRY_DISABLED=1
COPY frontend/package.json frontend/bun.lock ./
RUN bun install --frozen-lockfile --ignore-scripts
COPY frontend/ ./
RUN bun run build
RUN find node_modules -type f \( -iname 'LICENSE*' -o -iname 'COPYING*' -o -iname 'NOTICE*' \) \
    -exec sh -c 'for src do dst="/dependency-licenses/frontend/$src"; mkdir -p "$(dirname "$dst")"; cp "$src" "$dst"; done' sh {} +

FROM golang:1.27.3-alpine AS build
WORKDIR /src
ENV GOMAXPROCS=2 CGO_ENABLED=0
COPY go.mod go.sum ./
RUN go mod download
COPY *.go ./
COPY --from=frontend /src/web ./web
RUN go build -p 2 -trimpath -ldflags="-s -w" -o /torrent-drive .
RUN mkdir -p /dependency-licenses/go \
    && cp /usr/local/go/LICENSE /dependency-licenses/go/Go-LICENSE \
    && find /go/pkg/mod -type f \( -iname 'LICENSE*' -o -iname 'COPYING*' -o -iname 'NOTICE*' \) \
    -exec sh -c 'for src do dst="/dependency-licenses/go/${src#/go/pkg/mod/}"; mkdir -p "$(dirname "$dst")"; cp "$src" "$dst"; done' sh {} +

FROM alpine:3.23
RUN apk add --no-cache ca-certificates \
    && addgroup -g 10001 app \
    && adduser -D -u 10001 -G app app \
    && mkdir -p /data /run/torrent-drive /credentials \
    && chown -R app:app /data /run/torrent-drive /credentials \
    && chmod 0700 /data /credentials \
    && chmod 0750 /run/torrent-drive
COPY --from=build /torrent-drive /usr/local/bin/torrent-drive
COPY LICENSE THIRD_PARTY_NOTICES.md /usr/share/licenses/torrent-drive/
COPY licenses/ /usr/share/licenses/torrent-drive/
COPY --from=frontend /dependency-licenses/ /usr/share/licenses/torrent-drive/dependencies/
COPY --from=build /dependency-licenses/ /usr/share/licenses/torrent-drive/dependencies/
USER 10001:10001
ENTRYPOINT ["/usr/local/bin/torrent-drive"]
CMD ["serve", "--state", "/data", "--oauth", "/credentials/oauth.json"]
