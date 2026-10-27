---
title: VPN Routing
description: Route Torrent Drive torrent and Google Drive traffic through a Gluetun WireGuard VPN with Docker Compose.
---

# VPN routing

The default stack uses normal Internet routing. `compose.vpn.yaml` adds Gluetun
and runs the worker in its network namespace. Torrent traffic and Google Drive
traffic both go through the VPN. The web service still talks to the worker over
the Unix socket.

## Set up

1. Get a WireGuard configuration from your VPN provider.
2. Add the provider values to `.env`:

   ```bash
   VPN_SERVICE_PROVIDER=YOUR_GLUETUN_PROVIDER_NAME
   WIREGUARD_PRIVATE_KEY=YOUR_PRIVATE_KEY
   WIREGUARD_ADDRESSES=YOUR_PROVIDER_ADDRESS
   SERVER_COUNTRIES=YOUR_COUNTRY
   ```

3. Check the values for your provider in the
   [Gluetun documentation](https://github.com/qdm12/gluetun-wiki).
4. Start the VPN stack:

   ```bash
   docker compose -f compose.yaml -f compose.vpn.yaml up -d
   ```

Use both files for later `up`, `down`, and `logs` commands. The worker waits for
the VPN health check before it starts. After you replace the VPN container,
recreate the worker so it joins the new namespace.

The VPN needs a Linux TUN device and provider credentials. Do not remove the
VPN network mode to work around a connection failure.
