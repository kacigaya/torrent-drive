FROM oven/bun:1.4.4 AS frontend
WORKDIR /src/frontend
ENV NEXT_TELEMETRY_DISABLED=1
COPY frontend/package.json frontend/bun.lock ./
RUN bun install --frozen-lockfile --ignore-scripts
COPY frontend/ ./
RUN bun run build

FROM golang:1.27.3-alpine AS build
WORKDIR /src
ENV GOMAXPROCS=2 CGO_ENABLED=0
COPY go.mod go.sum ./
RUN go mod download
COPY cmd/ ./cmd/
COPY internal/ ./internal/
COPY --from=frontend /src/internal/web/dist ./internal/web/dist
RUN go build -p 2 -trimpath -ldflags="-s -w" -o /torrent-drive ./cmd/torrent-drive

FROM alpine:3.23
RUN apk add --no-cache ca-certificates \
    && addgroup -g 10001 app \
    && adduser -D -u 10001 -G app app \
    && mkdir -p /data /run/torrent-drive /credentials \
    && chown -R app:app /data /run/torrent-drive /credentials \
    && chmod 0700 /data /credentials \
    && chmod 0750 /run/torrent-drive
COPY --from=build /torrent-drive /usr/local/bin/torrent-drive
USER 10001:10001
ENTRYPOINT ["/usr/local/bin/torrent-drive"]
CMD ["serve", "--state", "/data", "--oauth", "/credentials/oauth.json"]
