---
title: Development
description: Build Torrent Drive from source, run its tests, and run the opt-in live Google Drive test.
---

# Development

## Build

Use Go 1.27, Bun 1.4.4, and a Linux host. Build the frontend before Go, because
the binary embeds it:

```bash
cd frontend
bun install --frozen-lockfile --ignore-scripts
bun test
bun run typecheck
NEXT_TELEMETRY_DISABLED=1 bun run build
cd ..
go test -p 2 ./...
go test -race -p 2 ./...
go vet ./...
GOMAXPROCS=2 CGO_ENABLED=0 go build -p 2 -o bin/torrent-drive ./cmd/torrent-drive
```

`internal/web/dist/` is generated output. Edit `frontend/` instead.

## Tests

The test suite needs no Google credentials. It uses real local torrent peers and
a fake Drive server. It covers:

1. Verified v1 and v2 peer transfers with RAM storage.
2. Piece eviction, late blocks, rewind, and corrupt data.
3. Upload retries, partial acknowledgements, checkpoints, and checksum failures.
4. Pause, retry, clear, and worker refresh state changes.
5. Browser origin checks, API routes, private headers, and CSP hashes.
6. Runtime settings and OAuth client and callback checks.

Large-torrent tests check metadata limits only. They do not transfer 200 GiB.

## Container checks

```bash
docker compose config --quiet
docker compose build
docker compose up -d
docker compose exec worker torrent-drive status
curl --fail http://127.0.0.1:8080/api/config
```

Do not use real credentials in CI, and do not upload runtime volumes as artifacts.

## Live Drive test

The live test creates real Drive files and never deletes them. Use a dedicated
folder and a worker that has no other jobs. The test generates CC0 text in RAM
and compares it with the SHA-256 and size that Drive reports.

```bash
export TORRENT_DRIVE_LIVE=1
export TORRENT_DRIVE_OAUTH=/path/to/private/oauth.json
export TORRENT_DRIVE_TEST_PARENT=YOUR_TEST_FOLDER_ID
go test ./internal/worker -run '^TestLiveDrive$' -v
```

The test uses the worker at the default control socket. Set
`TORRENT_DRIVE_VPN_DNS=1` only when both processes share Gluetun's network
namespace.
