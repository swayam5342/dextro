import { openAsBlob, statSync } from "node:fs";

const base = process.env.TRIBUTARY_URL;
const headers = { Authorization: `Bearer ${process.env.TRIBUTARY_KEY}` };

const PIECE = 8 * 1024 * 1024; // every piece but the last must be a multiple of 256 KiB
const path = "big.iso";
let uploadId = process.env.UPLOAD_ID; // set this to pick up an upload that stopped

async function json(response) {
  if (!response.ok) throw new Error(`HTTP ${response.status}`);
  return response.json();
}

if (!uploadId) {
  const started = await fetch(`${base}/api/v1/uploads`, {
    method: "POST",
    headers: { ...headers, "Content-Type": "application/json" },
    body: JSON.stringify({ name: path, size: statSync(path).size }),
  });
  uploadId = (await json(started)).id;
}
console.log("Upload", uploadId);

// Ask where things really stand, because the account may have kept less than was sent.
let session = await json(await fetch(`${base}/api/v1/uploads/${uploadId}`, { headers }));
const file = await openAsBlob(path);

while (!session.file) {
  const offset = session.received;
  session = await json(
    await fetch(`${base}/api/v1/uploads/${uploadId}/chunk?offset=${offset}`, {
      method: "PUT",
      headers,
      body: file.slice(offset, offset + PIECE),
    }),
  );
}
console.log(session.file.id);
