---
title: Quick Start
description: Run Torrent Drive with Docker Compose, connect Google Drive, and submit your first torrent.
---

# Quick start

## Requirements

- A Linux host with cgroup v2, Docker Engine, and Docker Compose.
- At least 1 GiB of free RAM for the application. Builds need more RAM and disk.
- A Google Drive account with enough free space for the final files.

The image supports Linux ARM64 and AMD64. Build it on the target platform.
Docker Desktop is not tested. The worker refuses to start when swap is enabled
without a zero-swap cgroup limit. Do not remove the Compose memory, swap, CPU,
or process limits.

## Steps

1. Clone the repository and copy the settings example:

   ```bash
   git clone https://github.com/kacigaya/torrent-drive.git
   cd torrent-drive
   cp .env.example .env
   docker compose build
   mkdir -m 700 secrets
   ```

2. Create a **Desktop app** OAuth client and save its JSON as
   `secrets/client.json`. See [Google Drive OAuth](/docs/getting-started/google-oauth/).

3. Connect your Google account. Open the URL that the command prints:

   ```bash
   docker compose --profile setup run --rm -T \
     -p 127.0.0.1:53682:53682 authorize < secrets/client.json
   ```

   On a remote server, open the [SSH tunnel](/docs/user-guide/remote-access/) first.

4. Start the worker and the dashboard:

   ```bash
   docker compose up -d
   ```

5. Open <http://127.0.0.1:8080>. Choose a Drive folder and submit a small legal
   torrent first.

The dashboard binds to host loopback, not to the public network. Next, read
[usage](/docs/user-guide/usage/) for settings and CLI commands.
