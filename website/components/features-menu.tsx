"use client";

import Link from "next/link";
import { useState } from "react";

const FEATURES = [
  { name: "Upload anything", text: "Send files or whole folders in pieces, so an upload can pause, resume and survive a lost connection. Several run at once, up to 3 per account, and you choose how they spread from the Upload menu.", image: "/media/uploading.webp", alt: "The upload panel with three files going up" },
  { name: "Encrypt a folder", text: "Choose a folder, and new files in it are encrypted before they reach Google, so only your server can read them. Viewing, downloads and moves keep working. Names stay visible in Drive, and encrypted files have no thumbnails or Google links.", image: "/media/encrypted.webp", alt: "An encrypted folder with a lock on its file" },
  { name: "Search like Gmail", text: "Type filters such as type:image, size:>10mb, after:2026-01-31 or owner:work, or pick them from a menu, then sort the results by name, date or size.", image: "/media/search.webp", alt: "Search results filtered to photos, with menus for type, size, date and account" },
  { name: "Preview in place", text: "Photos, video, PDFs, text and code open in the app, without downloading them first.", image: "/media/viewer.webp", alt: "A large photo open in the viewer with its details alongside" },
  { name: "Keep things tidy", text: "Real folders, stars, recent files, keyboard shortcuts, drag and drop, and a trash with Undo.", image: "/media/list.webp", alt: "Files in a list showing size, account and date" },
  { name: "Find duplicates", text: "See files with the same content, wherever they are, and how much space you'd get back. It goes by each file's hash, so nothing has to be downloaded.", image: "/media/duplicates.webp", alt: "Two copies of a file listed with how much space could be freed" },
  { name: "Catch problems early", text: "Tributary tells you when a file goes missing or changes in Drive, and can read files back on a schedule to confirm they're intact. You choose how often and how much, or turn it off.", image: "/media/checks.webp", alt: "The file checks card on the Storage page" },
  { name: "Share one file", text: "Make a public Google link for a single file, choose whether people can edit, and set when it ends. It's off until the admin turns it on.", image: "/media/link.webp", alt: "The Google link dialog with a choice of role and expiry" },
  { name: "Move between accounts", text: "Rebalance your accounts, or empty one before unlinking it. Each copy is checked before the old one goes.", image: "/media/storage.webp", alt: "Three accounts on the Storage page with files moving between them" },
  { name: "Use it on your phone", text: "The whole app works on a small screen, in light or dark.", image: "/media/phone.webp", alt: "The file browser on a phone" },
  { name: "Script it with a key", text: "Upload, read and organize files from your own code with a scoped API key.", image: "/media/files-dark.webp", alt: "The Tributary file browser", link: "/api-guide" },
];

/** What Tributary does, set out like the app's own menu. */
export function FeaturesMenu() {
  const [query, setQuery] = useState("");
  const [open, setOpen] = useState(FEATURES[0].name);
  const found = FEATURES.filter(({ name, text }) => `${name} ${text}`.toLowerCase().includes(query.trim().toLowerCase()));

  return (
    <section className="section" aria-labelledby="features-title" data-ui>
      <h2 id="features-title" data-rise>
        What it does
      </h2>
      <input
        className="menu-search"
        type="search"
        placeholder="Find a feature"
        aria-label="Find a feature"
        value={query}
        onChange={(event) => setQuery(event.target.value)}
      />
      <ul className="menu">
        {found.map(({ name, text, image, alt, link }) => (
          <li key={name}>
            {name === open || found.length === 1 ? (
              <div className="menu-open">
                <div>
                  <h3>{name}</h3>
                  <p>{text}</p>
                  {link && <Link href={link}>Read the API guide</Link>}
                </div>
                {/* eslint-disable-next-line @next/next/no-img-element */}
                <img src={image} alt={alt} loading="lazy" width={800} height={500} />
              </div>
            ) : (
              <button type="button" className="menu-row" onClick={() => setOpen(name)}>
                {name}
              </button>
            )}
          </li>
        ))}
      </ul>
      {found.length === 0 && <p className="menu-empty">Nothing matches that. Try "upload" or "search".</p>}
    </section>
  );
}
