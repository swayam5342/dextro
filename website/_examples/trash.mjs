const base = process.env.TRIBUTARY_URL;
const headers = { Authorization: `Bearer ${process.env.TRIBUTARY_KEY}` };
const fileId = process.env.FILE_ID;

async function post(path) {
  const response = await fetch(`${base}${path}`, { method: "POST", headers });
  if (!response.ok) throw new Error(`HTTP ${response.status}`);
}

await post(`/api/v1/files/${fileId}/trash`);

const trash = await (await fetch(`${base}/api/v1/trash`, { headers })).json();
console.log(trash.files.length, "files in the trash");

await post(`/api/v1/files/${fileId}/restore`);
