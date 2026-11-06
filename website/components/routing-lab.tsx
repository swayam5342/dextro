"use client";

import { useState } from "react";

type Mode = "most_space" | "round_robin" | "priority";

const LIMIT = 15;
const START = [
  { name: "north.fork", used: 9.2 },
  { name: "south.fork", used: 4.6 },
  { name: "little.creek", used: 1.4 },
];
const FILES = [
  { name: "Holiday video.mp4", size: 2.4 },
  { name: "Thesis scans.zip", size: 1.1 },
  { name: "Photos 2025.zip", size: 3.2 },
  { name: "Podcast archive.zip", size: 1.7 },
  { name: "Wedding film.mov", size: 4.1 },
];
const MODES: { mode: Mode; label: string; note: string }[] = [
  { mode: "most_space", label: "Most space", note: "Each file goes to the account with the most free space, so your accounts level out." },
  { mode: "round_robin", label: "Round robin", note: "Files take turns, so they spread across your accounts one by one." },
  { mode: "priority", label: "Priority order", note: "Files fill your first account, then move to the next when one won't fit." },
];

const free = (used: number) => LIMIT - used;

/** Which account takes a file, in the order the server tries them. */
function route(mode: Mode, used: number[], last: number | null, size: number): number | null {
  const all = used.map((_, index) => index);
  let tries = all;
  if (mode === "most_space") {
    tries = [...all].sort((a, b) => free(used[b]) - free(used[a]));
  } else if (mode === "round_robin" && last !== null) {
    tries = [...all.slice(last + 1), ...all.slice(0, last + 1)];
  }
  return tries.find((index) => free(used[index]) >= size) ?? null;
}

/** The three routing modes, working on three made-up accounts. */
export function RoutingLab() {
  const [mode, setMode] = useState<Mode>("most_space");
  const [used, setUsed] = useState(START.map((account) => account.used));
  const [last, setLast] = useState<number | null>(null);
  const [sent, setSent] = useState(0);
  const [log, setLog] = useState<string[]>([]);

  const file = FILES[sent % FILES.length];
  const next = route(mode, used, last, file.size);

  function send() {
    setSent(sent + 1);
    if (next === null) {
      setLog([`${file.name} has nowhere to go: no account has ${file.size} GB free.`, ...log].slice(0, 3));
      return;
    }
    setUsed(used.map((value, index) => (index === next ? value + file.size : value)));
    setLast(next);
    setLog([`${file.name}, ${file.size} GB, went to ${START[next].name}`, ...log].slice(0, 3));
  }

  function reset() {
    setUsed(START.map((account) => account.used));
    setLast(null);
    setSent(0);
    setLog([]);
  }

  return (
    <section id="routing" className="section lab" aria-labelledby="routing-title">
      <div className="lab-side">
        <h2 id="routing-title" data-rise>
          Choose where files go
        </h2>
        <p data-rise style={{ "--i": 1 } as React.CSSProperties}>
          Each person picks how their uploads are shared out. You can always send a file to one account yourself, and an
          account that's full or can't be reached is skipped.
        </p>
        <div className="modes" role="group" aria-label="Upload routing">
          {MODES.map(({ mode: option, label }) => (
            <button key={option} type="button" className="mode" aria-pressed={option === mode} onClick={() => setMode(option)}>
              <b>{label}</b>
            </button>
          ))}
        </div>
        <p className="mode-note">{MODES.find((option) => option.mode === mode)?.note}</p>
      </div>
      <div className="stage" data-ui data-rise style={{ "--i": 2 } as React.CSSProperties}>
        <ul className="tanks">
          {START.map(({ name }, index) => (
            <li key={name}>
              <div className="tank-figure">{free(used[index]).toFixed(1)} GB</div>
              <div className="tank-sub">free of {LIMIT} GB</div>
              <div className="tank-well" role="img" aria-label={`${name} is ${Math.round((used[index] / LIMIT) * 100)}% full`}>
                <span className="tank-water" style={{ height: `${(used[index] / LIMIT) * 100}%` }} />
              </div>
              <div className="tank-name">{name}</div>
              <div className="tank-next">{index === next ? "Next file goes here" : ""}</div>
            </li>
          ))}
        </ul>
        <div className="stage-bar">
          <button type="button" className="button" onClick={send}>
            Upload {file.name}
          </button>
          <button type="button" className="button soft" onClick={reset}>
            Start again
          </button>
        </div>
        <ul className="stage-log" aria-live="polite">
          {log.length === 0 && <li>Press upload and watch where the file goes, then try another mode.</li>}
          {log.map((line, index) => (
            <li key={`${sent}-${index}`}>{line}</li>
          ))}
        </ul>
      </div>
    </section>
  );
}
