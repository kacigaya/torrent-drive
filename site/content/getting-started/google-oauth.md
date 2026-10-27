---
title: Google Drive OAuth
description: Create a Google Desktop OAuth client, connect Torrent Drive to your Drive, or import an existing connection.
---

# Google Drive OAuth

## Create the client

1. Create or select a project in Google Cloud Console.
2. Enable the [Google Drive API](https://console.cloud.google.com/apis/library/drive.googleapis.com).
3. Configure the OAuth consent screen. For a testing app, add your account as
   a test user.
4. Create an OAuth client with application type **Desktop app**.
5. Download its JSON to `secrets/client.json` in the project directory.
6. Run the authorization command from the [quick start](/docs/getting-started/quick-start/).
7. Open the printed URL in your local browser and approve access. Check the
   terminal for the result.

The command uses PKCE, a random state, and a loopback redirect. The client JSON
goes through stdin. It is not copied into the Docker image.

## What is stored

The client asks for the full Drive scope. Folder browsing and uploads into
existing folders need more than access to files created by this app.

The refresh token is stored in the `credentials` Docker volume with mode `0600`.
Only the worker and the one-off authorization container mount that volume.
Tokens are never printed. Keep `secrets/` private.

## Replace a connection

The authorization command never overwrites an existing OAuth file. To replace
a connection:

1. Stop the stack.
2. Back up the credentials volume.
3. Move the old file aside in that volume.
4. Run the authorization command again.

Do not remove credentials during a running transfer.

Google applies consent-screen, verification, and token-lifetime rules. Refresh
tokens of apps in testing mode can expire. Read
[Google's Desktop OAuth guide](https://developers.google.com/identity/protocols/oauth2/native-app)
and the [refresh token expiration rules](https://developers.google.com/identity/protocols/oauth2#expiration)
before you rely on a connection for unattended work.

## Import an existing connection

You can import a connection instead of using browser authorization. The worker
expects this JSON:

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

Save it as `secrets/oauth.json`. Copy it privately into the credentials volume:

```bash
docker compose --profile setup run --rm -T --entrypoint sh authorize \
  -c 'umask 077; test ! -e /credentials/oauth.json && cat > /credentials/oauth.json' \
  < secrets/oauth.json
```

Do not paste credentials into issues, shell arguments, or public logs.
