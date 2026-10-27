---
title: How It Works
description: How Torrent Drive streams verified torrent pieces through a RAM window into Google Drive resumable uploads.
---

# How it works

## Transfer

1. The Go worker asks [anacrolix/torrent](https://github.com/anacrolix/torrent)
   for one piece at a time.
2. The library checks the piece hash. Only then can the worker read the bytes.
3. The worker copies verified bytes into an 8 MiB upload buffer.
4. It sends the buffer with Google's
   [resumable upload API](https://developers.google.com/workspace/drive/api/guides/manage-uploads).
5. It saves the acknowledged offset and a hash checkpoint as private metadata.

The RAM store holds one piece. When the next piece starts, the previous one is
released. No storage path exists for torrent payload.

## Verification

The final file keeps its original name, size, and contents. A transfer is
complete only when Drive reports the expected file ID, folder, size, and MD5
checksum. SHA-256 is also checked when Google returns it.

Multi-file torrents keep their folder tree. Drive IDs for files and folders are
reserved and saved before use, so a restart never creates duplicates. Finished
files can appear in Drive before the rest of the torrent finishes.

## Dashboard

Next.js builds a static React dashboard. The Go binary embeds it, serves its
assets, and forwards a small allowlist of API routes to the worker socket.
There is no Node.js server in production. The web container cannot read OAuth
credentials or job state.

## Code layout

| Path | Contents |
| --- | --- |
| `cmd/torrent-drive` | Command-line entry point and control-socket client |
| `internal/worker` | Job queue, run loop, and control API |
| `internal/swarm` | Torrent metadata checks, RAM piece store, and piece reader |
| `internal/drive` | Google Drive client and resumable uploads |
| `internal/jobs` | Job manifests and their on-disk store |
| `internal/web` | Dashboard gateway and embedded frontend build |
| `internal/auth` | One-time Google OAuth setup |
| `frontend` | Next.js dashboard source |
