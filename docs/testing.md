# Testing

## Local checks

Build the frontend first, as shown in the README. Then run its tests, TypeScript
checks, Go tests, race tests, and `go vet`. No Google credentials are needed.

The deterministic suite covers:

1. Verified v1 and v2 peer transfers with RAM storage.
2. Piece eviction, late blocks, rewind, and corruption handling.
3. Upload retries, partial acknowledgements, checkpoints, and checksum failures.
4. Pause, retry, clear, and worker refresh state transitions.
5. Browser origin checks, API routes, private headers, and CSP hashes.
6. Runtime settings validation and OAuth client/callback validation.

Large-torrent admission tests validate metadata limits. They do not transfer
200 GB. A full large transfer still depends on peers, storage capacity, API
quotas, and a final integrity check.

## Container checks

```sh
docker compose config --quiet
docker compose build
```

After authorizing your own Drive connection:

```sh
docker compose up -d
docker compose exec worker torrent-drive status
curl --fail http://127.0.0.1:8080/api/config
```

Check that the published UI port is loopback-only and that worker swap is zero.
Do not use real private credentials in CI or upload runtime volumes as artifacts.

## Live Drive test

Live tests create real Drive files. Use a dedicated folder and an isolated
worker. The test generates CC0 text in RAM. It then compares the SHA-256 and size that
Drive reports for each uploaded file with the generated text. It does not delete Drive files.

Set these environment variables for a native test process:

```sh
export TORRENT_DRIVE_LIVE=1
export TORRENT_DRIVE_OAUTH=/path/to/private/oauth.json
export TORRENT_DRIVE_TEST_PARENT=YOUR_DEDICATED_TEST_FOLDER_ID
go test ./internal/worker -run '^TestLiveDrive$' -v
```

The test expects a worker at the default control socket. Run it under the same
network and resource restrictions as that worker. Set
`TORRENT_DRIVE_VPN_DNS=1` only when both processes use Gluetun's network
namespace. Do not run this test against an active production queue.
