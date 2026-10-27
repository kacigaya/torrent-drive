---
title: Backups and Updates
description: Back up Torrent Drive state and credentials, recover interrupted transfers, and update the Docker stack.
---

# Backups and updates

## Volumes

| Volume | Contents |
| --- | --- |
| `state` | Job JSON, torrent metadata, Drive upload offsets, and hash checkpoints. No torrent payload. |
| `credentials` | The OAuth connection |
| `control` | The worker's Unix socket only |

## Back up

1. Stop the stack:

   ```bash
   docker compose stop
   ```

2. Back up the `state` and `credentials` volumes with your Docker volume backup
   tool. Encrypt the backups. Tracker URLs and upload sessions are secrets.
3. Start the stack again with `docker compose up -d`.

`docker compose down` keeps named volumes.
**`docker compose down -v` deletes job state and credentials.** Completed Drive
files stay, but unfinished uploads cannot resume.

## Recovery

After an interruption, the worker asks Drive for the upload cursor. It downloads
the needed pieces again and rebuilds the hash state. An expired Drive session
restarts that file from zero. Completed files stay intact. Multi-file transfers
are not atomic.

The worker exits with code 75 when the torrent client reaches its peer-data or
memory budget. Docker restarts it, and the job continues from its checkpoint.
Attempts without progress use failure backoff.

## Update

Back up the volumes first. Then run:

```bash
git pull --ff-only
docker compose build
docker compose up -d
docker compose logs --tail 30 worker web
```

Do not use a new state volume while a transfer is unfinished.
