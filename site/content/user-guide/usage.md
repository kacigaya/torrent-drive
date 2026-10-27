---
title: Usage
description: Use the Torrent Drive dashboard, configure optional settings, and control jobs from the CLI.
---

# Usage

## Dashboard

The dashboard accepts a magnet link or a `.torrent` file. You can:

- Choose a Drive folder for each transfer.
- Follow progress and speed.
- Pause, resume, or retry a transfer.
- Open verified results in Google Drive.
- Clear completed transfers from the list. The files stay in Drive.

Private trackers sometimes need the `.torrent` file, because peers do not share
magnet metadata. DHT and peer exchange stay off unless you allow public
discovery for a public magnet.

## Settings

Set these optional values in `.env`:

| Setting | Default | Purpose |
| --- | --- | --- |
| `PORT` | `8080` | Dashboard port on host loopback |
| `ORIGIN` | `http://127.0.0.1:8080` | Exact browser origin for Host and CSRF checks |
| `DRIVE_FOLDER_ID` | Empty | Preselect a destination folder |
| `QBITTORRENT_URL` | Empty | Add a link to your qBittorrent dashboard |

Run `docker compose up -d` to apply changes. When you change `PORT`, also change
the port in `ORIGIN`.

## CLI

The CLI talks to the worker through a private Unix socket:

```bash
docker compose exec worker torrent-drive status --watch
docker compose exec worker torrent-drive folders
docker compose exec worker torrent-drive pause --job JOB_ID
docker compose exec worker torrent-drive retry --job JOB_ID
```

Submit a torrent from the CLI:

```bash
docker compose exec worker torrent-drive submit --folder FOLDER_ID \
  --magnet 'magnet:?xt=urn:btih:...'
```

Add `--public-discovery` only for a public magnet with no tracker. Never use it
for a private torrent.
