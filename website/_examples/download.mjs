import { createWriteStream } from "node:fs";
import { Readable } from "node:stream";
import { pipeline } from "node:stream/promises";

const base = process.env.TRIBUTARY_URL;
const headers = { Authorization: `Bearer ${process.env.TRIBUTARY_KEY}` };

const response = await fetch(`${base}/api/v1/files/${process.env.FILE_ID}/content`, { headers });
if (!response.ok) throw new Error(`HTTP ${response.status}`);
await pipeline(Readable.fromWeb(response.body), createWriteStream("report.pdf"));
