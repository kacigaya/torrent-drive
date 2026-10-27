# Security

The dashboard is a single-user control surface. It has no built-in login.
Default Compose port publishing is limited to `127.0.0.1`. Keep that binding,
or put authenticated HTTPS or a private VPN in front of the dashboard.

The worker uses a private Unix socket. Any process that can connect to it can
submit work to the configured Google Drive account. Do not mount that socket
into unrelated containers.

Keep these private:

1. Google OAuth client files and refresh tokens.
2. The credentials and job-state volumes.
3. Torrent metadata and private tracker URLs.
4. Resumable upload session URLs and backups.
5. VPN keys and `.env` files.

The worker stores torrent payload only in its bounded RAM store. Compose sets
memory and swap limits. Do not remove them. Generated core dumps are disabled.
Do not add torrent disk storage or a caching cloud mount as a shortcut.

Host administrators and the container engine can access process memory and
private volumes. This application does not protect secrets from the host owner.

Report vulnerabilities through GitHub's private vulnerability reporting when
available. Do not include tokens, private torrents, or personal file names in
public issues. Revoke exposed OAuth or VPN credentials before sharing logs.
