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
  <a href="https://kacigaya.github.io/torrent-drive/"><strong>Documentation</strong></a> ·
  <a href="https://kacigaya.github.io/torrent-drive/docs/getting-started/quick-start/">Quick start</a> ·
  <a href="https://kacigaya.github.io/torrent-drive/docs/reference/limits/">Limits</a>
</p>

Torrent Drive downloads torrents straight into Google Drive. The worker fetches
one verified piece at a time into a fixed RAM window and streams it with
resumable uploads. Your server needs RAM for one piece, not disk for the file.

## Features

- Submit magnet links and `.torrent` files from a web dashboard or the CLI.
- Get the original files and folder tree in Google Drive.
- Verify torrent pieces before upload and Drive checksums after upload.
- Resume interrupted uploads from saved checkpoints.
- Run with Docker Compose on Linux ARM64 and AMD64, with an optional VPN.

## Quick start

```sh
git clone https://github.com/kacigaya/torrent-drive.git
cd torrent-drive
cp .env.example .env
docker compose build
mkdir -m 700 secrets   # save your Desktop OAuth client as secrets/client.json
docker compose --profile setup run --rm -T \
  -p 127.0.0.1:53682:53682 authorize < secrets/client.json
docker compose up -d
```

Open <http://127.0.0.1:8080>. The [quick start guide](https://kacigaya.github.io/torrent-drive/docs/getting-started/quick-start/)
explains each step, including [Google OAuth](https://kacigaya.github.io/torrent-drive/docs/getting-started/google-oauth/)
and [remote servers](https://kacigaya.github.io/torrent-drive/docs/user-guide/remote-access/).

AI coding agents can set it up for you with the
[`torrent-drive-setup` skill](.agents/skills/torrent-drive-setup/SKILL.md).
It is also linked from `.claude/skills/` and `.codex/skills/`.

The dashboard has no login. Keep it on loopback or behind an authenticated
proxy. Read [SECURITY.md](SECURITY.md).

## License

[MIT](LICENSE).
