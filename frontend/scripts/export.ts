import { cp, mkdir, rename, rm } from "node:fs/promises";
import { fileURLToPath } from "node:url";

const output = fileURLToPath(new URL("../out/", import.meta.url));
const target = fileURLToPath(new URL("../../internal/web/dist/", import.meta.url));
const stage = `${target.replace(/\/$/, "")}.next`;
await rm(stage, { recursive: true, force: true });
await mkdir(stage, { recursive: true });
for (const file of ["index.html", "favicon.svg", "robots.txt", "_next/static"]) {
  await cp(`${output}/${file}`, `${stage}/${file}`, { recursive: true });
}
await rm(target, { recursive: true, force: true });
await rename(stage, target);
console.log("Next.js export copied to internal/web/dist/ for the Go gateway.");
