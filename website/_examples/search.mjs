const base = process.env.TRIBUTARY_URL;
const headers = { Authorization: `Bearer ${process.env.TRIBUTARY_KEY}` };

const query = new URLSearchParams({ q: "report" });
const response = await fetch(`${base}/api/v1/search?${query}`, { headers });
if (!response.ok) throw new Error(`HTTP ${response.status}`);
const results = await response.json();

for (const file of results.files) console.log(file.name, file.id);
