# Third-party notices

Torrent Drive's original code uses the MIT license. Dependencies and copied
components retain their own licenses.

## coss ui

The files in `frontend/components/ui/` are adapted from coss ui's MIT-licensed
component registry. They build on Base UI. Only component source is included,
not the upstream AGPL application.

Sources:

- [Component README](https://github.com/cosscom/ui/blob/main/apps/ui/README.md)
- [Upstream licensing boundaries](https://github.com/cosscom/ui/blob/main/LICENSING.md)
- [Retained MIT notice](licenses/coss-ui-MIT.txt)

## Inter

The dashboard uses Inter, self-hosted by the Next.js build. It is distributed
under the SIL Open Font License 1.1. Its copyright and license are retained in
[licenses/Inter-OFL.txt](licenses/Inter-OFL.txt).

## Package dependencies

The worker links [anacrolix/torrent v1.61.0](https://github.com/anacrolix/torrent/tree/v1.61.0),
which uses the Mozilla Public License 2.0. It is used as an unmodified library.
Its source remains available at the linked upstream version.

Exact Go dependencies are recorded in `go.mod` and `go.sum`. Exact frontend
dependencies are recorded in `frontend/bun.lock`. Their upstream license files
remain part of the corresponding source distributions. They are not relicensed
by the top-level MIT license.

The Docker build collects available dependency license, copying, and notice
files under `/usr/share/licenses/torrent-drive/dependencies/`. The top-level
project, component, and font licenses are in `/usr/share/licenses/torrent-drive/`.
