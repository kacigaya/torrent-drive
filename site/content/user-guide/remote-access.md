---
title: Remote Access
description: Reach a Torrent Drive dashboard on a remote server through an SSH tunnel or an authenticated HTTPS reverse proxy.
---

# Remote access

The dashboard has no login screen. Its Host and CSRF checks are not user
authentication. Anyone who can reach the dashboard can use your Drive.

## SSH tunnel

The dashboard and the OAuth callback listen on host loopback. On your own
computer, run:

```bash
ssh -L 53682:127.0.0.1:53682 -L 8080:127.0.0.1:8080 USER@SERVER
```

Keep this connection open. Run the OAuth command in the remote shell and open
the printed URL in your local browser. The callback reaches the remote container
through the tunnel. After `docker compose up -d`, open <http://127.0.0.1:8080>
on your computer.

Do not publish the OAuth callback port on a public interface.

## HTTPS reverse proxy

1. Choose a domain and an HTTPS proxy.
2. Set `ORIGIN=https://drive.example.com` in `.env`.
3. Keep the Compose port bound to `127.0.0.1`.
4. Add authentication in the proxy. The
   [Caddy example](https://github.com/kacigaya/torrent-drive/blob/main/deploy/Caddyfile.example)
   uses a password hash. Create it with `caddy hash-password` and give it to the
   proxy as `DASHBOARD_PASSWORD_HASH`.
5. Run `docker compose up -d` and configure the TLS certificate.

If the proxy runs in another container, host loopback is not its loopback.
Create a private Docker network and route to the web service there. Keep the
worker socket and state off that network.
