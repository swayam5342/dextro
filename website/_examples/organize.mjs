const base = process.env.TRIBUTARY_URL;
const headers = { Authorization: `Bearer ${process.env.TRIBUTARY_KEY}` };
const fileId = process.env.FILE_ID;

async function send(method, path, body) {
  const response = await fetch(`${base}${path}`, {
    method,
    headers: { ...headers, "Content-Type": "application/json" },
    body: JSON.stringify(body),
  });
  if (!response.ok) throw new Error(`HTTP ${response.status}`);
  return response.json();
}

const folder = await send("POST", "/api/v1/folders", { name: "Reports", parent_id: null });
await send("POST", `/api/v1/files/${fileId}/move`, { folder_id: folder.id });
await send("PATCH", `/api/v1/files/${fileId}`, { name: "March report.pdf" });
