"use client";

import Link from "next/link";
import { useEffect, useState } from "react";

import type { DocGroup } from "@/components/docs-shell";

/** The sidebar. It marks the section being read, and folds away on a phone. */
export function DocsMenu({
  pages,
  current,
  groups,
}: {
  pages: { title: string; links: { href: string; label: string }[] }[];
  current: string;
  groups: DocGroup[];
}) {
  const [active, setActive] = useState(groups[0].items[0][0]);
  const [open, setOpen] = useState(true);

  useEffect(() => {
    const narrow = matchMedia("(max-width: 900px)");
    setOpen(!narrow.matches);
    const headings = groups.flatMap((group) => group.items).flatMap(([id]) => document.getElementById(id) ?? []);
    const watcher = new IntersectionObserver(
      (entries) => {
        const seen = entries.filter((entry) => entry.isIntersecting).sort((a, b) => a.boundingClientRect.top - b.boundingClientRect.top);
        if (seen[0]) {
          setActive(seen[0].target.id);
        }
      },
      { rootMargin: "0px 0px -75% 0px" },
    );
    headings.forEach((heading) => watcher.observe(heading));
    return () => watcher.disconnect();
  }, [groups]);

  function closeOnPhone() {
    if (matchMedia("(max-width: 900px)").matches) {
      setOpen(false);
    }
  }

  return (
    <details className="docs-menu" open={open} onToggle={(event) => setOpen(event.currentTarget.open)}>
      <summary>On this page</summary>
      <nav aria-label="Documentation">
        {pages.map(({ title, links }) => (
          <div key={title}>
            <h2>{title}</h2>
            <ul>
              {links.map(({ href, label }) => (
                <li key={href}>
                  <Link href={href} aria-current={href === current ? "page" : undefined}>
                    {label}
                  </Link>
                </li>
              ))}
            </ul>
          </div>
        ))}
        {groups.map(({ title, items }) => (
          <div key={title}>
            <h2>{title}</h2>
            <ul>
              {items.map(([id, label]) => (
                <li key={id}>
                  <a href={`#${id}`} aria-current={id === active ? "location" : undefined} onClick={closeOnPhone}>
                    {label}
                  </a>
                </li>
              ))}
            </ul>
          </div>
        ))}
      </nav>
    </details>
  );
}
