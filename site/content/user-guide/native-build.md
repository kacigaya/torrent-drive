---
title: Native Build
description: Build and run the Torrent Drive worker and dashboard without Docker.
---

# Native build

Docker Compose is the recommended path, because it sets the resource limits.
To run without Docker:

1. Build the binary as shown in [development](/docs/development/).
2. Create private directories and authorize:

   ```bash
   mkdir -p secrets data run
   chmod 700 secrets data run
   bin/torrent-drive authorize --client secrets/client.json --oauth secrets/oauth.json
   ```

3. Start the worker:

   ```bash
   bin/torrent-drive serve --state "$PWD/data" --socket "$PWD/run/control.sock" \
     --oauth "$PWD/secrets/oauth.json"
   ```

4. In a second terminal, start the dashboard:

   ```bash
   bin/torrent-drive web --listen 127.0.0.1:8080 --origin http://127.0.0.1:8080 \
     --socket "$PWD/run/control.sock"
   ```

Disable swap or use a zero-swap cgroup. Set RAM, CPU, process, and restart
limits with your service manager. Restart the worker when it exits with code 75.
