# Self-hosting

## Requirements

1. Use a Linux host with cgroup v2 and Docker Engine with Compose.
2. Allow at least 1 GiB free RAM for the application. Building needs more RAM
   and disk space for toolchains and image layers.
3. Use a Google Drive account with enough capacity for the final files.

The image supports Linux ARM64 and AMD64 through its base images. Build it on
the target platform with `docker compose build`. Docker Desktop is not tested.
The process refuses to run when it detects swap without a zero-swap cgroup
limit. Do not remove Compose memory, swap, CPU, or process limits.

## Google Drive OAuth

1. Create or select a project in Google Cloud Console.
2. Enable the [Google Drive API](https://console.cloud.google.com/apis/library/drive.googleapis.com).
3. Configure the OAuth consent screen. For a testing app, add your account as
   a test user.
4. Create an OAuth client with application type **Desktop app**.
5. Download its JSON to `secrets/client.json` in the project directory.
6. Run the authorization command from the README. It uses PKCE, a random state,
   and a loopback redirect. The client JSON is passed through stdin. It is not
   copied into the Docker image.
7. Open the printed URL locally. Approve access to your Drive. Return to the
   terminal and check that the connection was saved.

The client requests the Drive scope because folder browsing and uploads into
existing folders need access beyond files created by this app. The refresh token
is stored in the `credentials` Docker volume with mode `0600`. Only the worker
and the one-off authorization container mount that volume. Tokens are never
printed. Keep `secrets/` private and outside backups intended for publication.

The authorization command refuses to overwrite an existing OAuth file. To
replace a connection, stop the stack and back up the credentials volume first.
Then move the old file aside in that volume and repeat setup. Do not remove
credentials from a running transfer.

Google applies consent-screen, verification, and token-lifetime rules to OAuth
apps. Testing-mode refresh tokens can expire. Review
[Google's Desktop OAuth guide](https://developers.google.com/identity/protocols/oauth2/native-app)
and [refresh token expiration rules](https://developers.google.com/identity/protocols/oauth2#expiration)
before relying on a connection for unattended work.

### Existing OAuth connection

You can import an existing connection instead of running browser authorization.
The worker expects this JSON structure:

```json
{
  "client_id": "YOUR_DESKTOP_CLIENT_ID",
  "client_secret": "YOUR_DESKTOP_CLIENT_SECRET",
  "token": {
    "refresh_token": "YOUR_REFRESH_TOKEN",
    "token_type": "Bearer"
  }
}
```

Store it at `secrets/oauth.json`. Copy it privately into the credentials volume:

```sh
docker compose --profile setup run --rm -T --entrypoint sh authorize \
  -c 'umask 077; test ! -e /credentials/oauth.json && cat > /credentials/oauth.json' \
  < secrets/oauth.json
```

The import does not change rclone or any other application's configuration.
Do not paste credentials into issues, shell arguments, or public logs.

## Remote server

The default UI and OAuth callback listen on host loopback. Before authorizing
on a remote Linux server, run this on your own computer:

```sh
ssh -L 53682:127.0.0.1:53682 -L 8080:127.0.0.1:8080 USER@SERVER
```

Keep that connection open. In the remote shell, run the OAuth command from the
README. Open the printed Google URL in your local browser. The callback reaches
the remote container through the tunnel. After `docker compose up -d`, open
`http://127.0.0.1:8080` locally.

Use another SSH session or terminal multiplexer if needed. Do not publish the
OAuth callback port on a public interface.

## HTTPS reverse proxy

The app has no login screen. Put authentication or a private network in front
of any remotely reachable dashboard. Its Host and CSRF checks are not user
authentication.

1. Choose your domain and HTTPS proxy.
2. Set `ORIGIN=https://drive.example.com` in your private `.env`.
3. Keep the Compose port bound to `127.0.0.1`.
4. Use [the Caddy example](../deploy/Caddyfile.example) on the same host. It
   requires a password hash. Generate one with `caddy hash-password` and pass
   it as the proxy's private `DASHBOARD_PASSWORD_HASH` environment variable.
5. Run `docker compose up -d` and configure your proxy's TLS certificate.

If the proxy runs in another container, host loopback is not its loopback.
Create a private shared Docker network and route to the web service there.
Keep the worker socket and state off that proxy network.

## Optional VPN

`compose.vpn.yaml` adds Gluetun and routes the worker through its network
namespace. Both torrent traffic and Google Drive traffic use that namespace.
The web service still talks to the worker over the Unix socket.

1. Obtain WireGuard configuration from your VPN provider.
2. Add these provider values to your private `.env`:

   ```dotenv
   VPN_SERVICE_PROVIDER=YOUR_GLUETUN_PROVIDER_NAME
   WIREGUARD_PRIVATE_KEY=YOUR_PRIVATE_KEY
   WIREGUARD_ADDRESSES=YOUR_PROVIDER_ADDRESS
   SERVER_COUNTRIES=YOUR_COUNTRY
   ```

3. Check your provider's settings in the
   [Gluetun documentation](https://github.com/qdm12/gluetun-wiki).
4. Start the VPN stack:

   ```sh
   docker compose -f compose.yaml -f compose.vpn.yaml up -d
   ```

Use both files for subsequent `up`, `down`, and `logs` commands. The worker
waits for the VPN container's health check before starting. After replacing a
VPN container, recreate its worker so it joins the new namespace. Do not remove
the VPN network mode to work around a connection failure.

The optional VPN needs a Linux TUN device and provider credentials. It is not
needed for the default loopback-only UI setup.

## Recovery and backups

The `state` volume holds private job JSON, torrent metadata, acknowledged Drive
offsets, and hash checkpoints. It holds no torrent payload. The `control` volume
holds only a Unix socket. The `credentials` volume holds the OAuth connection.

1. Stop the stack before taking a consistent backup:

   ```sh
   docker compose stop
   ```

2. Back up the state and credentials volumes with your normal Docker volume
   backup tool. Encrypt them. Treat tracker URLs and upload sessions as secrets.
3. Start again with `docker compose up -d`.

`docker compose down` keeps named volumes. **`docker compose down -v` deletes
local job state and credentials.** It does not delete completed Drive files,
but the app can no longer resume unfinished uploads from that deleted state.

After interruption, the worker checks Drive's upload cursor. It downloads any
needed pieces again and rebuilds hash state. Expired Drive sessions require
that file to restart from zero. Completed files remain intact. Multi-file
transfers are not atomic.

The torrent client exits with code 75 after its peer-data or heap budget is
reached. Docker's `on-failure` policy restarts it. No-progress attempts use
failure backoff. Job progress remains in the state volume.

## Updates

```sh
git pull --ff-only
docker compose build
docker compose up -d
docker compose logs --tail 30 worker web
```

Back up private volumes first. Do not use a new state volume for an unfinished
transfer. All users of the dashboard share its single Drive connection.

## Native build

Build the frontend and Go binary as shown in the README. Authorize and run:

```sh
mkdir -p secrets data run
chmod 700 secrets data run
bin/torrent-drive authorize --client secrets/client.json --oauth secrets/oauth.json
bin/torrent-drive serve --state "$PWD/data" --socket "$PWD/run/control.sock" \
  --oauth "$PWD/secrets/oauth.json"
```

In a second terminal:

```sh
bin/torrent-drive web --listen 127.0.0.1:8080 --origin http://127.0.0.1:8080 \
  --socket "$PWD/run/control.sock"
```

Native operation requires disabled swap or a zero-swap cgroup. Apply equivalent
RAM, CPU, process, and restart limits with your service manager. Compose is the
recommended path because it supplies those limits.
