import { describe, expect, spyOn, test } from "bun:test";
import { api, folderID, folderUrl, parseConfig, parseFolders, parseJobs, rate } from "./transfers";

test("runtime settings support a fresh self-hosted installation", () => {
  expect(parseConfig({ default_folder: "", qbittorrent_url: "" })).toEqual({ default_folder: "", qbittorrent_url: "" });
  expect(folderUrl("")).toBe("https://drive.google.com/drive/u/0/my-drive");
  expect(() => parseConfig({ default_folder: 123 })).toThrow();
});

test("retains worker validation errors and gateway JSON errors", async () => {
  const fetch = spyOn(globalThis, "fetch");
  try {
    fetch.mockResolvedValueOnce(new Response("torrent metadata is invalid\n", { status: 400 }));
    await expect(api("/jobs", {})).rejects.toThrow("torrent metadata is invalid");
    fetch.mockResolvedValueOnce(new Response(JSON.stringify({ error: "Downloader unavailable" }), { status: 503 }));
    await expect(api("/jobs")).rejects.toThrow("Downloader unavailable");
  } finally { fetch.mockRestore(); }
});

describe("Drive destinations", () => {
  test("accepts folder IDs and normal Drive folder links", () => {
    expect(folderID("abc_-123")).toBe("abc_-123");
    expect(folderID("https://drive.google.com/drive/u/0/folders/abc?usp=sharing")).toBe("abc");
    expect(folderID("https://drive.google.com/drive/folders/abc")).toBe("abc");
  });
  test("rejects lookalike hosts, credentials, root, and non-folder paths", () => {
    for (const value of ["root", "https://drive.google.com.evil.test/drive/folders/abc", "http://drive.google.com/drive/folders/abc", "https://user@drive.google.com/drive/folders/abc", "https://drive.google.com/file/d/abc", "<script>"]) expect(folderID(value)).toBeNull();
  });
  test("sorts names and handles empty Drive folders", () => {
    expect(parseFolders({}).files).toEqual([]);
    expect(parseFolders({ files: [{ id: "b", name: "Zulu" }, { id: "a", name: "Alpha" }], nextPageToken: "next" })).toEqual({ files: [{ id: "a", name: "Alpha" }, { id: "b", name: "Zulu" }], truncated: true });
    expect(() => parseFolders({ files: [{ id: "a" }] })).toThrow();
  });
});
describe("transfer progress", () => {
  test("resets speed on rewind or reconnect", () => {
    expect(rate([{ time: 0, uploaded: 100 }], { time: 6000, uploaded: 40 }).speed).toBeNull();
    expect(rate([{ time: 0, uploaded: 100 }], { time: 20000, uploaded: 500 }).speed).toBeNull();
    expect(rate([{ time: 0, uploaded: 100 }], { time: 10000, uploaded: 10100 }).speed).toBe(1000);
  });
  test("rejects malformed jobs before rendering progress or links", () => {
    expect(parseJobs([])).toEqual([]);
    expect(() => parseJobs([{ id: "unknown", uploaded_bytes: -1 }])).toThrow();
    expect(() => parseJobs({ jobs: [] })).toThrow();
  });
});
