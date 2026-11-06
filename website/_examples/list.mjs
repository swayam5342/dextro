const base = process.env.TRIBUTARY_URL;
const headers = { Authorization: `Bearer ${process.env.TRIBUTARY_KEY}` };

const response = await fetch(`${base}/api/v1/files`, { headers });
if (!response.ok) throw new Error(`HTTP ${response.status}`);
const listing = await response.json();

for (const folder of listing.folders) console.log("folder", folder.name, folder.id);
for (const file of listing.files) console.log("file", file.name, file.size, file.id);
