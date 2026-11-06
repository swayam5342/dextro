import { fileURLToPath } from "node:url";

import { createApp, parseTrustedProxies } from "./app.mjs";

const port = Number(process.env.PORT ?? 3000);
const apiUrl = new URL(process.env.DEXTRO_API_URL ?? "http://localhost:8080");
const root = fileURLToPath(new URL("../dist", import.meta.url));

const proxies = parseTrustedProxies(process.env.DEXTRO_TRUSTED_PROXIES);

const server = await createApp({ root, apiUrl, proxies });

server.listen(port, () => {
  console.log(`web server listening on port ${port}, proxying the API to ${apiUrl.origin}`);
});

for (const signal of ["SIGINT", "SIGTERM"]) {
  process.on(signal, () => {
    server.close(() => process.exit(0));
    server.closeIdleConnections();
  });
}
