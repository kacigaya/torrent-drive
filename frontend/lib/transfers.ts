export type RuntimeConfig = { default_folder: string; qbittorrent_url: string };

export function parseConfig(value: unknown): RuntimeConfig {
  if (!record(value) || typeof value.default_folder !== "string" || typeof value.qbittorrent_url !== "string") {
    throw new Error("The dashboard returned invalid settings.");
  }
  return { default_folder: value.default_folder, qbittorrent_url: value.qbittorrent_url };
}

export type ResultFile = { path: string; drive_id: string; size: number; sha256: string };
export type Job = {
  id: string; name: string; status: string; folder: string;
  uploaded_bytes: number; total_bytes: number; verified_files: number; files: number;
  error?: string; retry_at?: string; results?: ResultFile[];
};
export type Folder = { id: string; name: string };
export type Sample = { time: number; uploaded: number };

export function record(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}
function count(value: unknown): value is number {
  return typeof value === "number" && Number.isSafeInteger(value) && value >= 0;
}
function isResult(value: unknown): value is ResultFile {
  return record(value) && typeof value.path === "string" && typeof value.drive_id === "string" &&
    count(value.size) && typeof value.sha256 === "string";
}
function isJob(value: unknown): value is Job {
  return record(value) && typeof value.id === "string" && /^[a-f0-9]{32}$/.test(value.id) &&
    typeof value.name === "string" && typeof value.status === "string" && typeof value.folder === "string" &&
    count(value.uploaded_bytes) && count(value.total_bytes) && count(value.files) && count(value.verified_files) &&
    (value.error === undefined || typeof value.error === "string") &&
    (value.retry_at === undefined || typeof value.retry_at === "string") &&
    (value.results === undefined || (Array.isArray(value.results) && value.results.every(isResult)));
}
export function parseJobs(value: unknown): Job[] {
  if (!Array.isArray(value) || !value.every(isJob)) throw new Error("The downloader returned an invalid transfer list.");
  return value;
}
export function parseFolders(value: unknown): { files: Folder[]; truncated: boolean } {
  if (!record(value)) throw new Error("Drive returned an invalid folder list.");
  const files = value.files ?? [];
  if (!Array.isArray(files) || !files.every((f): f is Folder => record(f) && typeof f.id === "string" && typeof f.name === "string")) {
    throw new Error("Drive returned an invalid folder list.");
  }
  return { files: files.toSorted((a, b) => a.name.localeCompare(b.name)), truncated: Boolean(value.nextPageToken) };
}
export async function api(path: string, body?: object, signal?: AbortSignal): Promise<unknown> {
  const response = await fetch(`/api${path}`, {
    method: body ? "POST" : "GET",
    headers: body ? { Accept: "application/json", "Content-Type": "application/json", "X-Torrent-Drive": "1" } : { Accept: "application/json" },
    body: body ? JSON.stringify(body) : undefined,
    cache: "no-store", credentials: "same-origin",
    signal: signal ? AbortSignal.any([signal, AbortSignal.timeout(45000)]) : AbortSignal.timeout(45000),
  });
  const text = await response.text();
  let data: unknown = null;
  try { data = text ? JSON.parse(text) : null; } catch { /* Worker validation errors use plain text. */ }
  if (!response.ok) throw new Error(record(data) && typeof data.error === "string" ? data.error : text.trim().slice(0, 2000) || `Request failed (${response.status}).`);
  return data;
}
export function message(error: unknown) { return error instanceof Error ? error.message : "The request failed. Try again."; }
export function folderID(text: string): string | null {
  const value = text.trim();
  if (/^[A-Za-z0-9_-]{1,256}$/.test(value) && value !== "root") return value;
  try {
    const url = new URL(value);
    if (url.protocol !== "https:" || url.hostname !== "drive.google.com" || url.username || url.password) return null;
    const id = url.pathname.match(/^\/drive\/(?:u\/\d+\/)?folders\/([A-Za-z0-9_-]+)\/?$/)?.[1] ??
      url.pathname.match(/^\/folders\/([A-Za-z0-9_-]+)\/?$/)?.[1];
    return id && id !== "root" && id.length <= 256 ? id : null;
  } catch { return null; }
}
export function bytes(value: number): string {
  if (!Number.isFinite(value) || value <= 0) return "0 B";
  const units = ["B", "KiB", "MiB", "GiB", "TiB"];
  const index = Math.min(Math.floor(Math.log(value) / Math.log(1024)), units.length - 1);
  return `${new Intl.NumberFormat("en", { maximumFractionDigits: index > 1 ? 2 : 0 }).format(value / 1024 ** index)} ${units[index]}`;
}
export function rate(samples: Sample[], sample: Sample): { history: Sample[]; speed: number | null } {
  const previous = samples.at(-1);
  const valid = previous && (previous.uploaded > sample.uploaded || sample.time - previous.time > 15000) ? [] : samples;
  const history = [...valid.filter(s => sample.time - s.time <= 20000), sample];
  const first = history[0];
  const elapsed = sample.time - first.time;
  return { history, speed: elapsed >= 5000 ? Math.max(0, (sample.uploaded - first.uploaded) * 1000 / elapsed) : null };
}
export function duration(seconds: number): string {
  if (!Number.isFinite(seconds) || seconds <= 0) return "Calculating";
  const minutes = Math.ceil(seconds / 60);
  if (minutes < 60) return `${minutes} min`;
  const hours = Math.floor(minutes / 60);
  return `${hours} h ${minutes % 60} min`;
}
export function folderUrl(id: string) { return id ? `https://drive.google.com/drive/folders/${encodeURIComponent(id)}` : "https://drive.google.com/drive/u/0/my-drive"; }
