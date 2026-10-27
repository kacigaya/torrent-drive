<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="docs/branding/logo-white.svg">
    <img src="docs/branding/logo-black.svg" alt="Torrent Drive logo" width="140">
  </picture>
</p>

<h1 align="center">Torrent Drive</h1>

<p align="center">
  <strong>Download torrents to Google Drive with bounded RAM.</strong><br>
  <em>Original files. No payload on disk.</em>
</p>

<p align="center">
  <a href="https://go.dev"><img alt="Go 1.27" src="https://shieldcn.dev/badge/Go-1.27-171717.svg?variant=secondary&amp;logo=go&amp;logoColor=ffffff"></a>
  <a href="https://nextjs.org"><img alt="Next.js 16.4.3" src="https://shieldcn.dev/badge/Next.js-16.4.3-171717.svg?variant=secondary&amp;logo=nextdotjs&amp;logoColor=ffffff"></a>
  <a href="https://bun.sh"><img alt="Bun 1.4.4" src="https://shieldcn.dev/badge/Bun-1.4.4-171717.svg?variant=secondary&amp;logo=bun&amp;logoColor=ffffff"></a>
</p>

<p align="center">
  <a href="#quick-start">Quick start</a> ·
  <a href="#how-it-works">How it works</a> ·
  <a href="docs/self-hosting.md">Self-hosting</a> ·
  <a href="#branding">Brand assets</a>
</p>

## Features

- Submit magnet links and `.torrent` files from a web dashboard or CLI.
- Save single-file and multi-file torrents as normal Google Drive files.
- Browse Drive folders and choose a destination for each transfer.
- Verify torrent pieces before upload and file checksums after upload.
- Resume interrupted uploads from saved Drive checkpoints.
- Keep torrent payload in bounded RAM. Disk stores private metadata only.
- Run with Docker Compose on Linux, including ARM64.
- Use a neutral interface with light and dark themes.

## Quick start

Requirements: a Linux host, Docker Engine, Docker Compose, and enough free
Google Drive storage. The Docker configuration limits RAM and disables swap
for each application container. Read the [limits](#limits) before starting.

1. Clone the repository and copy the settings example:

   ```sh
   git clone https://github.com/kacigaya/torrent-drive.git
   cd torrent-drive
   cp .env.example .env
   docker compose build
   mkdir -m 700 secrets
   ```

2. Enable the Google Drive API in your Google Cloud project. Create a
   **Desktop app** OAuth client. Save its downloaded JSON as
   `secrets/client.json`. See the [OAuth setup guide](docs/self-hosting.md#google-drive-oauth).

3. Connect your Google account. Open the URL printed in the terminal:

   ```sh
   docker compose --profile setup run --rm -T \
     -p 127.0.0.1:53682:53682 authorize < secrets/client.json
   ```

   On a remote server, first create the SSH tunnel described in the
   [setup guide](docs/self-hosting.md#remote-server).

4. Start the worker and dashboard:

   ```sh
   docker compose up -d
   ```

5. Open <http://127.0.0.1:8080>. Choose a Drive folder and submit a small legal
   torrent first. The dashboard is bound to host loopback, not the public network.

Google Drive capacity and API quotas still apply. This project does not provide
free cloud storage. Each installation uses its own Google OAuth connection.

## Usage

The dashboard accepts a magnet link or `.torrent` file. You can choose a Drive
folder, follow progress, pause or resume work, and open verified results.
Completed transfers can be cleared from the list without deleting Drive files.

Optional settings in `.env`:

| Setting | Default | Purpose |
| --- | --- | --- |
| `PORT` | `8080` | Dashboard port on host loopback |
| `ORIGIN` | `http://127.0.0.1:8080` | Exact browser origin for Host and CSRF checks |
| `DRIVE_FOLDER_ID` | Empty | Preselect a destination folder |
| `QBITTORRENT_URL` | Empty | Add a link to your existing qBittorrent dashboard |

Changes take effect after `docker compose up -d`. When changing `PORT`, also
change the port in `ORIGIN`. No domain or Drive folder is built into the image.

The CLI reaches the worker through a private Unix socket:

```sh
docker compose exec worker torrent-drive status --watch
docker compose exec worker torrent-drive folders
docker compose exec worker torrent-drive pause --job JOB_ID
docker compose exec worker torrent-drive retry --job JOB_ID
```

For native builds, OAuth import, reverse proxies, updates, backups, and optional
VPN routing, see [self-hosting](docs/self-hosting.md).

## How it works

The Go worker asks anacrolix/torrent for one piece at a time. The library checks
its integrity. The worker copies verified bytes into a bounded upload buffer,
then uses Google's resumable upload API. It saves acknowledged offsets and hash
checkpoints as private metadata.

The final file retains its original name, size, and contents. Completion
requires a matching Drive file identity, destination, size, and MD5 checksum.
SHA-256 is also checked when Google returns it. Multi-file torrents keep their
directory tree. Finished files can appear before the rest of a torrent finishes.

Next.js builds a static React dashboard. Go serves its exported assets and
proxies a small set of API routes through the worker socket. There is no Node.js
server in production. The web container cannot read OAuth credentials or job
state. The source [brand kit](docs/branding/README.md) documents the visual identity.

## Limits

- One active torrent, up to 256 GiB, with at most 4,096 files.
- One piece up to 64 MiB and an 8 MiB upload buffer.
- Worker: 768 MiB RAM, no swap, two CPUs, and 128 processes or threads.
- Dashboard: 128 MiB RAM, no swap, and half of one CPU.
- Torrent download and Drive upload: 16 MiB/s each.
- At most 32 established peers and four connection attempts at once.
- At most 16 retained jobs and 128 MiB of private job metadata.
- No seeding, WebTorrent, webseeds, or symlink torrents.

Sequential piece requests can be slower than a normal torrent client. The
worker does not seed completed data. Check private tracker rules before use.
Private trackers sometimes require the actual `.torrent` file because peers
do not share magnet metadata. DHT and peer exchange stay off unless explicitly
allowed for a public magnet. A missing piece cannot be skipped.

RAM is bounded independently of total file size. This makes files larger than
local free disk possible. It does not guarantee transfer speed or peer availability.
Long interruptions can expire a Drive upload session and require a fresh upload
of that file. Finished files remain intact. See [recovery](docs/self-hosting.md#recovery-and-backups).

The default Compose stack uses normal Internet routing. It does not include a
VPN connection. The [optional Gluetun setup](docs/self-hosting.md#optional-vpn)
routes both torrent and Drive traffic through your provider.

## Development

Use Go 1.27, Bun 1.4.4, and a Linux host. Build the frontend before compiling Go:

```sh
cd frontend
bun install --frozen-lockfile --ignore-scripts
bun test
bun run typecheck
NEXT_TELEMETRY_DISABLED=1 bun run build
cd ..
go test -p 2 ./...
go test -race -p 2 ./...
go vet ./...
GOMAXPROCS=2 CGO_ENABLED=0 go build -p 2 -o bin/torrent-drive .
```

`web/` is generated output. Edit `frontend/` instead. The test suite uses real
local torrent peers and simulated Drive responses. Live Drive tests are opt-in
and require your own credentials and dedicated test folder. See
[testing](docs/testing.md).

## Branding

Use the [monochrome logos and wordmarks](docs/branding/README.md). The README
follows the centered logo, short description, and Shieldcn badge format used
in the maintainer's other projects.

## Security

The application has no built-in user authentication. Keep the default loopback
binding. Use an SSH tunnel, a VPN, or an authenticated HTTPS reverse proxy for
remote access. Anyone with dashboard or control-socket access can use the
connected Drive account. Never commit `.env`, credentials, state, or private
torrent files. Read [SECURITY.md](SECURITY.md).

## License

[MIT](LICENSE). Dependencies retain their [own licenses](THIRD_PARTY_NOTICES.md).
