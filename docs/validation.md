# Validation

The public self-hosting build was tested on Linux ARM64. Tests used isolated
containers and synthetic credentials. No deployed-instance state was imported.

1. Bun tests and TypeScript checks passed.
2. Next.js static export, Go tests, race checks, `go vet`, and the static Go
   build passed.
3. A clean multi-stage Docker build passed. Base and optional VPN Compose
   configuration checks passed.
4. The container dashboard served its page, assets, runtime configuration, and
   job API. Host spoofing, cross-site reads, and writes without the required
   CSRF headers were rejected.
5. Container inspection confirmed non-root users, read-only roots, loopback-only
   publishing, memory/CPU/process limits, and zero swap allocation. The web
   container did not receive credentials or job-state volumes.
6. Paused test-job metadata survived a worker restart.
7. The one-off OAuth container accepted client JSON through stdin. Its URL used
   PKCE and offline access. The loopback callback rejected an invalid state.
8. Publication checks excluded runtime files, local configuration, generated
   output, credentials, private torrents, and deployment identifiers.

The full Google consent flow was not repeated for publication. Live Drive
tests require the operator's own account. A complete 200 GB transfer and a real
VPN provider connection were not part of this publication check. AMD64 support
is supplied by the base images but was not exercised on this ARM64 host.

The frontend dependency scan reported no vulnerabilities in 139 exact locked
packages. The 76-module Go scan reported
[GO-2026-5932](https://osv.dev/vulnerability/GO-2026-5932) in
`golang.org/x/crypto v0.57.0`: its unmaintained OpenPGP package. That package is
not imported by this application's build. The advisory lists no fixed version.
