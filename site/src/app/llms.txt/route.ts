import { getDoc } from "@/lib/docs";
import { NAV } from "@/lib/docs-nav";
import { canonicalUrl, DEFAULT_DESCRIPTION, SITE_NAME } from "@/lib/seo";

export const dynamic = "force-static";

// An llms.txt index (https://llmstxt.org): one link per doc page, with its description.
export async function GET() {
  const lines = [`# ${SITE_NAME}`, "", `> ${DEFAULT_DESCRIPTION}`, ""];
  lines.push(
    "Self-host it with Docker Compose. Agents can follow the setup skill at",
    "https://github.com/kacigaya/torrent-drive/blob/main/.agents/skills/torrent-drive-setup/SKILL.md",
    "",
  );
  for (const section of NAV) {
    lines.push(`## ${section.title ?? "Overview"}`, "");
    for (const item of section.items) {
      const slug = item.href.replace(/^\/docs\/?/, "").split("/").filter(Boolean);
      const doc = await getDoc(slug);
      const url = canonicalUrl(`${item.href}/`);
      lines.push(`- [${item.title}](${url})${doc?.description ? `: ${doc.description}` : ""}`);
    }
    lines.push("");
  }
  lines.push("## Optional", "", "- [Source code](https://github.com/kacigaya/torrent-drive)", "");
  return new Response(lines.join("\n"), { headers: { "Content-Type": "text/plain; charset=utf-8" } });
}
