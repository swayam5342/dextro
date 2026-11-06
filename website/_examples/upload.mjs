import { openAsBlob } from "node:fs";

const base = process.env.TRIBUTARY_URL;
const headers = { Authorization: `Bearer ${process.env.TRIBUTARY_KEY}` };

// Needs Node 20 or later. The blob streams from disk.
const body = await openAsBlob("report.pdf");
const response = await fetch(`${base}/api/v1/files?name=report.pdf`, {
  method: "POST",
  headers: { ...headers, "Content-Type": "application/pdf" },
  body,
});
if (!response.ok) throw new Error(`HTTP ${response.status}`);
console.log((await response.json()).id);
