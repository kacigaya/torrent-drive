---
title: Security
description: Torrent Drive security model, access rules, and how to report a vulnerability.
---

# Security

Torrent Drive has no built-in user authentication. Anyone with access to the
dashboard or the control socket can use the connected Drive account.

- Keep the default loopback binding.
- For remote access, use an SSH tunnel, a VPN, or an authenticated HTTPS
  reverse proxy. See [remote access](/docs/user-guide/remote-access/).
- Never commit `.env`, credentials, job state, or private torrent files.
- The web container never mounts credentials or job state.
- The dashboard sends a strict Content Security Policy, checks Host and Origin,
  and forwards only an allowlist of API routes.

Report vulnerabilities as described in
[SECURITY.md](https://github.com/kacigaya/torrent-drive/blob/main/SECURITY.md).
