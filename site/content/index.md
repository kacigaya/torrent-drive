---
title: "Torrent Drive Docs: Torrents to Google Drive"
description: Docs for Torrent Drive, a self-hosted downloader that moves verified torrent pieces through bounded RAM into Google Drive.
---

# Torrent Drive documentation

Torrent Drive downloads torrents into Google Drive. It never writes torrent
payload to local disk. The worker requests one piece at a time, waits for the
torrent library to verify it, and uploads the bytes with Google's resumable
upload API. Your server needs RAM for one piece, not disk space for the file.

## Features

- Submit magnet links and `.torrent` files from a web dashboard or the CLI.
- Save single-file and multi-file torrents as normal Google Drive files.
- Browse Drive folders and choose a destination for each transfer.
- Verify torrent pieces before upload and file checksums after upload.
- Resume interrupted uploads from saved Drive checkpoints.
- Keep torrent payload in bounded RAM. Disk stores private metadata only.
- Run with Docker Compose on Linux, including ARM64.

## Start here

1. Follow the [quick start](/docs/getting-started/quick-start/) to run the stack.
2. Create your [Google Drive OAuth client](/docs/getting-started/google-oauth/).
3. Read the [limits](/docs/reference/limits/) before a large transfer.

Google Drive capacity and API quotas still apply. This project does not provide
free cloud storage. Each installation uses its own Google OAuth connection.
