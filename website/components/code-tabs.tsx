"use client";

import { useState, useSyncExternalStore } from "react";

export type Sample = { label: string; code: string };

const KEY = "tributary.language";
const listeners = new Set<() => void>();
let chosen: string | null = null;

function remembered(): string | null {
  if (chosen === null) {
    try {
      chosen = localStorage.getItem(KEY);
    } catch {
      // It just won't be remembered.
    }
  }
  return chosen;
}

function choose(label: string) {
  chosen = label;
  try {
    localStorage.setItem(KEY, label);
  } catch {
    // It just won't be remembered.
  }
  listeners.forEach((listener) => listener());
}

function subscribe(listener: () => void) {
  listeners.add(listener);
  return () => listeners.delete(listener);
}

/** A code sample. Choosing a language changes every sample on the page, and is remembered. */
export function CodeTabs({ samples }: { samples: Sample[] }) {
  const language = useSyncExternalStore(subscribe, remembered, () => null);
  const [copied, setCopied] = useState(false);
  const shown = samples.find((sample) => sample.label === language) ?? samples[0];

  async function copy() {
    await navigator.clipboard.writeText(shown.code);
    setCopied(true);
    setTimeout(() => setCopied(false), 1500);
  }

  return (
    <div className="code">
      <div className="code-bar">
        <div role="tablist" aria-label="Language">
          {samples.map((sample) => (
            <button key={sample.label} type="button" role="tab" className="code-tab" aria-selected={sample === shown} onClick={() => choose(sample.label)}>
              {sample.label}
            </button>
          ))}
        </div>
        <button type="button" className="code-copy" onClick={copy}>
          {copied ? "Copied" : "Copy"}
        </button>
      </div>
      <pre tabIndex={0}>
        <code>{shown.code}</code>
      </pre>
    </div>
  );
}
