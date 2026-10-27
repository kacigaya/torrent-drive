export const SITE_URL = "https://kacigaya.github.io/torrent-drive";
export const SITE_NAME = "Torrent Drive";
export const DEFAULT_TITLE = "Torrent Drive: Torrents to Google Drive with Bounded RAM";
export const DEFAULT_DESCRIPTION =
  "Self-hosted downloader that streams verified torrent pieces through bounded RAM into Google Drive, with no torrent payload on local disk.";

export const SEO_KEYWORDS = [
  "torrent to Google Drive",
  "self-hosted torrent downloader",
  "Google Drive resumable upload",
  "seedbox alternative",
  "BitTorrent",
  "Docker Compose",
  "Torrent Drive",
];

export function canonicalUrl(path = "/"): string {
  const normalized = path.startsWith("/") ? path : `/${path}`;
  return `${SITE_URL}${normalized}`;
}
