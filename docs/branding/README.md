# Torrent Drive brand kit

**Torrent Drive** saves torrent downloads as normal Google Drive files.
Use the name as two words. Use `torrent-drive` for commands and service names.

## Message

1. Main description: **Download torrents to Google Drive with bounded RAM.**
2. Short line: **Original files. No payload on disk.**
3. Extended description: **A self-hosted torrent downloader with verified
   pieces, resumable Drive uploads, and a private web dashboard.**

Keep claims specific. Bounded RAM does not mean zero memory. The service stores
metadata on disk. A large torrent still needs peers and enough Drive capacity.
Do not claim a completed 200 GB transfer without a verified result.

## Assets

| Asset | Light surface | Dark surface |
| --- | --- | --- |
| Logo | [Black SVG](logo-black.svg) | [White SVG](logo-white.svg) |
| Horizontal wordmark | [Black SVG](wordmark-black.svg) | [White SVG](wordmark-white.svg) |

The existing mark stays unchanged: three peers join a download arrow above a
storage tray. The SVGs have transparent backgrounds. They use no scripts,
external images, or remote fonts. Wordmarks use Inter with an Arial fallback.

1. Use black on light surfaces and white on dark surfaces.
2. Keep the aspect ratio. Do not add a colored tile, gradient, or shadow.
3. Leave space around the logo of at least one peer-dot diameter.
4. Use the logo at 20 px or larger for normal interface controls.
5. Keep an accessible product name beside a decorative logo. Otherwise give
   the image the alternative text `Torrent Drive`.

## Interface

| Element | Treatment |
| --- | --- |
| Logo | Black in light mode; white in dark mode |
| Brand links and progress | Foreground color |
| Success indicators | Neutral gray with explicit status text |
| Focus rings | Neutral 600 in light mode; neutral 400 in dark mode |
| Product text | Inter |
| IDs, paths, and checksums | System monospace |

The neutral palette keeps progress and file names prominent. Red and amber
remain available for errors and warnings. Do not restore green accents.

## README format

The project follows the format used in
[b!nje](https://github.com/kacigaya/binje),
[DNSSwitcher](https://github.com/kacigaya/dns-switcher), and
[Clean TweetX](https://github.com/kacigaya/clean-tweetx).

1. Center a theme-aware logo above the project title.
2. Add a short description and one supporting line.
3. Center three to six clickable Shieldcn badges with `variant=secondary`.
4. Show features before usage and installation.
5. Keep operating limits, recovery behavior, and setup commands accurate.

The badges show the actual toolchain. No license, CI, release, or popularity
badge is included without a corresponding repository fact.
