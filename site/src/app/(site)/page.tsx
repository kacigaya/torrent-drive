import { SiteFooter } from "@/components/site-footer";
import { SiteHeader } from "@/components/site-header";
import Link from "next/link";
import { Button } from "@/components/ui/button";
import {
  Card,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import { CodeBlock } from "@/components/code-block";
import { Logo } from "@/components/logo";
import { DEFAULT_DESCRIPTION, SITE_URL } from "@/lib/seo";

const DOCS_URL = "/docs";
const GITHUB_URL = "https://github.com/kacigaya/torrent-drive";

const FEATURES = [
  {
    title: "No payload on disk",
    description:
      "Only the requested piece lives in RAM. Disk holds private job metadata and nothing else.",
  },
  {
    title: "Original files",
    description:
      "Single and multi-file torrents arrive in Drive as normal files, with the same names and folder tree.",
  },
  {
    title: "Verified end to end",
    description:
      "Torrent piece hashes are checked before upload. Drive MD5 and SHA-256 are checked after.",
  },
  {
    title: "Resumable",
    description:
      "Acknowledged Drive offsets and hash checkpoints survive restarts, so uploads continue where they stopped.",
  },
  {
    title: "Web dashboard",
    description:
      "Submit magnets or .torrent files, browse Drive folders, and follow progress in light or dark theme.",
  },
  {
    title: "Private by default",
    description:
      "DHT and peer exchange stay off unless you allow them. The dashboard binds to loopback only.",
  },
  {
    title: "Docker Compose",
    description:
      "Two small containers with RAM, CPU, and swap limits. Works on Linux ARM64 and AMD64.",
  },
  {
    title: "Optional VPN",
    description:
      "An extra Compose file routes torrent and Drive traffic through Gluetun.",
  },
];

const INSTALL = `git clone https://github.com/kacigaya/torrent-drive.git
cd torrent-drive
cp .env.example .env
docker compose build
docker compose up -d`;

const SOFTWARE_SCHEMA = {
  "@context": "https://schema.org",
  "@type": "SoftwareApplication",
  name: "Torrent Drive",
  applicationCategory: "UtilitiesApplication",
  operatingSystem: "Linux",
  programmingLanguage: "Go",
  description: DEFAULT_DESCRIPTION,
  url: SITE_URL,
  codeRepository: GITHUB_URL,
  license: "https://opensource.org/license/mit",
  author: {
    "@type": "Person",
    name: "Gaya KACI",
    url: "https://github.com/kacigaya",
  },
  offers: {
    "@type": "Offer",
    price: "0",
    priceCurrency: "USD",
  },
};

export default function Home() {
  return (
    <>
      <SiteHeader />

      <main id="main-content" tabIndex={-1} className="flex flex-1 flex-col">
        <script
          type="application/ld+json"
          dangerouslySetInnerHTML={{ __html: JSON.stringify(SOFTWARE_SCHEMA) }}
        />

        <section className="mx-auto flex w-full max-w-5xl flex-col items-center px-6 py-24 text-center">
          <Logo className="mb-8 size-24" />
          <Badge variant="secondary" className="mb-6">
            Self-hosted · Bounded RAM · Docker
          </Badge>
          <h1 className="text-balance font-heading text-5xl font-bold tracking-tight sm:text-6xl">
            Torrents to Google Drive, with no payload on disk
          </h1>
          <p className="mt-6 max-w-2xl text-pretty text-lg text-muted-foreground">
            Torrent Drive downloads one verified piece at a time into a fixed
            RAM window and streams it to Google Drive with resumable uploads.
            You get the original files in Drive, even when they are larger than
            your server&apos;s free disk.
          </p>
          <div className="mt-10 flex flex-col gap-3 sm:flex-row">
            <Button size="xl" render={<Link href={DOCS_URL} />}>
              Read the docs
            </Button>
            <Button size="xl" variant="outline" render={<a href={GITHUB_URL} />}>
              View on GitHub
            </Button>
          </div>
        </section>

        <section className="mx-auto w-full max-w-3xl px-6 pb-24">
          <CodeBlock code={INSTALL} lang="bash" />
          <p className="mt-4 text-center text-sm text-muted-foreground">
            Connect your Google account first. The{" "}
            <Link href="/docs/getting-started/quick-start/" className="underline underline-offset-4 hover:text-foreground">
              quick start
            </Link>{" "}
            covers every step.
          </p>
        </section>

        <section className="mx-auto w-full max-w-5xl px-6 pb-24">
          <h2 className="mb-10 text-balance text-center font-heading text-3xl font-bold tracking-tight">
            Built for large transfers on small servers
          </h2>
          <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
            {FEATURES.map((feature) => (
              <Card key={feature.title}>
                <CardHeader>
                  <CardTitle render={<h3 />}>{feature.title}</CardTitle>
                  <CardDescription render={<p />}>
                    {feature.description}
                  </CardDescription>
                </CardHeader>
              </Card>
            ))}
          </div>
        </section>
      </main>

      <SiteFooter />
    </>
  );
}
