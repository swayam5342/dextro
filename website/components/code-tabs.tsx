"use client";

import { useState } from "react";

export type Sample = { label: string; code: string };

/** A code sample in several languages. The language chosen is kept across the page. */
export function CodeTabs({ samples }: { samples: Sample[] }) {
  const [label, setLabel] = useState(samples[0].label);
  const [copied, setCopied] = useState(false);
  const shown = samples.find((sample) => sample.label === label) ?? samples[0];

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
            <button key={sample.label} type="button" role="tab" className="code-tab" aria-selected={sample.label === label} onClick={() => setLabel(sample.label)}>
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
