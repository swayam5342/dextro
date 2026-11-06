"use client";

import { usePathname } from "next/navigation";
import { useEffect } from "react";

/** Brings each part of a section in as it arrives. Without a script, nothing is hidden. */
export function Reveal() {
  const pathname = usePathname();
  useEffect(() => {
    document.documentElement.classList.add("js");
    const parts = document.querySelectorAll<HTMLElement>("[data-rise]:not(.in)");
    const watcher = new IntersectionObserver(
      (entries) => {
        for (const entry of entries) {
          if (entry.isIntersecting) {
            entry.target.classList.add("in");
            watcher.unobserve(entry.target);
          }
        }
      },
      { rootMargin: "0px 0px -8% 0px" },
    );
    parts.forEach((part) => watcher.observe(part));
    return () => watcher.disconnect();
  }, [pathname]);
  return null;
}
