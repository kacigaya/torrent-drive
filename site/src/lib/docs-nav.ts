interface NavItem {
  title: string;
  href: string;
}

interface NavSection {
  title?: string;
  items: NavItem[];
}

export const NAV: NavSection[] = [
  { items: [{ title: "Home", href: "/docs" }] },
  {
    title: "Getting Started",
    items: [
      { title: "Quick Start", href: "/docs/getting-started/quick-start" },
      { title: "Google Drive OAuth", href: "/docs/getting-started/google-oauth" },
    ],
  },
  {
    title: "User Guide",
    items: [
      { title: "Usage", href: "/docs/user-guide/usage" },
      { title: "Remote Access", href: "/docs/user-guide/remote-access" },
      { title: "VPN Routing", href: "/docs/user-guide/vpn" },
      { title: "Backups and Updates", href: "/docs/user-guide/backups-and-updates" },
      { title: "Native Build", href: "/docs/user-guide/native-build" },
    ],
  },
  {
    title: "Reference",
    items: [
      { title: "How It Works", href: "/docs/reference/how-it-works" },
      { title: "Limits", href: "/docs/reference/limits" },
      { title: "Security", href: "/docs/reference/security" },
      { title: "Development", href: "/docs/development" },
    ],
  },
];

// Every markdown doc slug derived from the nav (["/docs"] -> []).
export function getDocSlugs(): string[][] {
  return NAV.flatMap((section) =>
    section.items.map((item) =>
      item.href.replace(/^\/docs\/?/, "").split("/").filter(Boolean),
    ),
  );
}
