import Link from "next/link";
import type { ReactNode } from "react";

import { DocsMenu } from "@/components/docs-menu";
import { DOCKER_HUB, GITHUB, SiteNav } from "@/components/site";

export type DocGroup = { title: string; items: [id: string, label: string][] };

const PAGES = [
  {
    title: "Guides",
    links: [
      { href: "/self-host", label: "Self-hosting" },
      { href: "/api-guide", label: "API guide" },
    ],
  },
  {
    title: "Legal",
    links: [
      { href: "/privacy", label: "Privacy policy" },
      { href: "/terms", label: "Terms of service" },
    ],
  },
];

/** The frame around every documentation page: a sidebar, one column of text, a small footer. */
export function DocsShell({
  current,
  title,
  lead,
  groups,
  next,
  children,
}: {
  current: string;
  title: string;
  lead: string;
  groups: DocGroup[];
  next: { href: string; label: string };
  children: ReactNode;
}) {
  return (
    <>
      <SiteNav />
      <div className="docs">
        <aside className="docs-side">
          <DocsMenu pages={PAGES} current={current} groups={groups} />
        </aside>
        <main className="docs-main">
          <h1>{title}</h1>
          <p className="docs-lead">{lead}</p>
          {children}
          <div className="docs-next">
            <Link href={next.href}>Next: {next.label}</Link>
          </div>
        </main>
      </div>
      <footer className="docs-foot">
        <span>Tributary, under the MIT licence</span>
        <nav aria-label="Footer">
          <a href={GITHUB}>GitHub</a>
          <a href={DOCKER_HUB}>Docker Hub</a>
          <Link href="/privacy">Privacy</Link>
          <Link href="/terms">Terms</Link>
        </nav>
      </footer>
    </>
  );
}
