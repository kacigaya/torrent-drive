---
name: torrent-drive-setup
description: Use when a user asks to install, configure, self-host, update, back up, troubleshoot, or expose Torrent Drive (torrents to Google Drive with bounded RAM). Covers Docker Compose, Google OAuth, settings, VPN, remote access, and health checks. Not for changing the Go or frontend code.
---

# Torrent Drive setup

Torrent Drive downloads torrents into Google Drive. A worker container holds one
verified piece in RAM and uploads it with Drive resumable uploads. A web
container serves the dashboard on host loopback. Full docs:
<https://kacigaya.github.io/torrent-drive/docs/>. The same pages are in
`site/content/` in this repository.

## Rules

- The user must do the Google steps in a browser: Cloud Console and the consent
  screen. Give exact steps. Never ask for passwords, tokens, or client secrets in
  chat, and never print them.
- Keep the dashboard bound to `127.0.0.1`. It has no login. For remote access,
  use an SSH tunnel or an authenticated HTTPS proxy. Never publish ports 8080 or
  53682 on a public interface.
- Do not remove the Compose memory, swap, CPU, or process limits.
- Never run `docker compose down -v` unless the user asks to delete job state and
  credentials. Back up volumes before updates.
- Never commit `.env`, `secrets/`, or `.torrent` files.
- Ask before a step that changes the host outside the project: installing Docker,
  firewall rules, a reverse proxy, or DNS.

## Workflow

1. **Check the host.** Confirm Linux, cgroup v2, and Docker Compose. Check free
   RAM (at least 1 GiB) and swap:

   ```bash
   uname -sm; stat -fc %T /sys/fs/cgroup   # expect cgroup2fs
   docker compose version
   free -h; swapon --show
   ```

   Host swap is fine. Compose sets a zero-swap limit for the containers.

2. **Get the code and settings.**

   ```bash
   git clone https://github.com/kacigaya/torrent-drive.git
   cd torrent-drive
   cp .env.example .env
   mkdir -m 700 secrets
   docker compose build
   ```

3. **Set `.env`.** Ask the user only for values they want to change:

   | Key | Default | Notes |
   | --- | --- | --- |
   | `PORT` | `8080` | Host loopback port. Change `ORIGIN` to match. |
   | `ORIGIN` | `http://127.0.0.1:8080` | Exact browser origin. Use the HTTPS URL behind a proxy. |
   | `DRIVE_FOLDER_ID` | empty | Preselected Drive folder ID from its URL. |
   | `QBITTORRENT_URL` | empty | Optional link. No credentials or query string. |

4. **Google OAuth client.** Tell the user to:
   1. Open Google Cloud Console and create or pick a project.
   2. Enable the Google Drive API.
   3. Configure the consent screen. In testing mode, add their account as a test user.
   4. Create an OAuth client of type **Desktop app**.
   5. Save the downloaded JSON as `secrets/client.json` on the server.

   Wait until `secrets/client.json` exists. Do not read its contents.

5. **Authorize.** On a remote server, the user must first open this tunnel from
   their own computer and keep it open:

   ```bash
   ssh -L 53682:127.0.0.1:53682 -L 8080:127.0.0.1:8080 USER@SERVER
   ```

   Then run this on the server. Give the printed URL to the user to open in
   their local browser:

   ```bash
   docker compose --profile setup run --rm -T \
     -p 127.0.0.1:53682:53682 authorize < secrets/client.json
   ```

   It prints "OAuth connection saved" on success. It never overwrites an
   existing connection. To import an existing token instead, follow
   `site/content/getting-started/google-oauth.md`.

6. **Start and verify.**

   ```bash
   docker compose up -d
   docker compose ps                       # worker and web healthy
   docker compose exec worker torrent-drive status
   docker compose exec worker torrent-drive folders
   curl --fail http://127.0.0.1:${PORT:-8080}/api/config
   ```

   `folders` proves Drive access. Ask the user to open the dashboard and submit
   a small legal torrent first.

## Optional tasks

- **VPN:** add Gluetun values to `.env` (`VPN_SERVICE_PROVIDER`,
  `WIREGUARD_PRIVATE_KEY`, `WIREGUARD_ADDRESSES`, `SERVER_COUNTRIES`). Then use
  both files for every command:
  `docker compose -f compose.yaml -f compose.vpn.yaml up -d`. Needs `/dev/net/tun`.
- **HTTPS proxy:** set `ORIGIN=https://DOMAIN`, keep the port on loopback, and
  require authentication. Start from `deploy/Caddyfile.example`. See
  `site/content/user-guide/remote-access.md`.
- **Update:** back up, then
  `git pull --ff-only && docker compose build && docker compose up -d`.
- **Back up:** `docker compose stop`, back up the `state` and `credentials`
  volumes encrypted, then `docker compose up -d`.

## Troubleshooting

| Symptom | Cause and fix |
| --- | --- |
| `cannot read OAuth credentials` | Authorization not done. Run step 5. |
| Worker says swap is enabled | The container has no zero-swap limit. Use the provided Compose files unchanged. |
| Dashboard `421` or `403` | `ORIGIN` does not match the browser URL or port. Fix `.env`, then `docker compose up -d`. |
| Dashboard `503` | The worker is down or restarting. Check `docker compose logs --tail 50 worker`. |
| Worker exits with code 75 | Normal memory refresh. Docker restarts it and the job resumes. |
| Magnet stays without metadata | Private tracker. Submit the `.torrent` file instead. |
| `invalid_grant` or refresh errors | Testing-mode token expired. Move the old OAuth file aside in the `credentials` volume and authorize again. |

Read logs with `docker compose logs --tail 50 worker web`. Logs never include
tokens or tracker URLs. Do not paste credentials from `.env` into reports.
