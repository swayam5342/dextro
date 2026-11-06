import assert from "node:assert/strict";
import { mkdir, mkdtemp, rm, writeFile } from "node:fs/promises";
import http from "node:http";
import { tmpdir } from "node:os";
import path from "node:path";
import { after, before, describe, it } from "node:test";

import { createApp } from "./app.mjs";

const silent = { error() {} };

function listen(server) {
  return new Promise((resolve) => {
    server.listen(0, "127.0.0.1", () => resolve(server.address().port));
  });
}

/** Sends a raw path, because fetch would normalize `..` before sending it. */
function rawGet(port, target) {
  return new Promise((resolve, reject) => {
    http
      .get({ port, host: "127.0.0.1", path: target }, (res) => {
        const chunks = [];
        res.on("data", (chunk) => chunks.push(chunk));
        res.on("end", () => resolve({ status: res.statusCode, body: Buffer.concat(chunks).toString() }));
      })
      .on("error", reject);
  });
}

describe("web server", () => {
  let dir;
  let api;
  let apiPort;
  let web;
  let base;
  let lastApiRequest;

  before(async () => {
    dir = await mkdtemp(path.join(tmpdir(), "dextro-web-"));
    const root = path.join(dir, "dist");
    await mkdir(path.join(root, "assets"), { recursive: true });
    await writeFile(path.join(root, "index.html"), "<title>app</title>");
    await writeFile(path.join(root, "favicon.svg"), "<svg/>");
    await writeFile(path.join(root, "drive-picker.html"), "<title>picker</title>");
    await writeFile(path.join(root, ".hidden"), "hidden");
    await writeFile(path.join(root, "assets", "app-1.js"), "console.log(1)");
    await writeFile(path.join(dir, "outside.txt"), "outside the root");

    api = http.createServer((req, res) => {
      const chunks = [];
      req.on("data", (chunk) => chunks.push(chunk));
      req.on("end", () => {
        lastApiRequest = { method: req.method, url: req.url, headers: req.headers, body: Buffer.concat(chunks).toString() };
        res.writeHead(req.url === "/api/v1/missing" ? 404 : 200, {
          "Content-Type": "application/json",
          "Set-Cookie": "session=abc; HttpOnly",
        });
        res.end('{"from":"api"}');
      });
    });
    apiPort = await listen(api);

    web = await createApp({ root, apiUrl: new URL(`http://127.0.0.1:${apiPort}`), logger: silent });
    base = `http://127.0.0.1:${await listen(web)}`;
  });

  after(async () => {
    web.closeAllConnections();
    api.closeAllConnections();
    await Promise.all([new Promise((r) => web.close(r)), new Promise((r) => api.close(r))]);
    await rm(dir, { recursive: true });
  });

  it("serves index.html for page paths, uncached", async () => {
    for (const target of ["/", "/index.html", "/files/some/folder"]) {
      const res = await fetch(base + target);
      assert.equal(res.status, 200, target);
      assert.equal(await res.text(), "<title>app</title>");
      assert.equal(res.headers.get("cache-control"), "no-cache");
      assert.match(res.headers.get("content-type"), /text\/html/);
    }
  });

  it("caches hashed assets for good", async () => {
    const res = await fetch(`${base}/assets/app-1.js`);
    assert.equal(await res.text(), "console.log(1)");
    assert.match(res.headers.get("cache-control"), /immutable/);
    assert.match(res.headers.get("content-type"), /text\/javascript/);
  });

  it("serves other files without long caching", async () => {
    const res = await fetch(`${base}/favicon.svg`);
    assert.equal(await res.text(), "<svg/>");
    assert.equal(res.headers.get("cache-control"), null);
  });

  it("returns a real 404 for a missing asset", async () => {
    const res = await fetch(`${base}/assets/old.js`);
    assert.equal(res.status, 404);
    assert.equal((await res.json()).error.code, "not_found");
  });

  it("answers HEAD without a body", async () => {
    const res = await fetch(`${base}/assets/app-1.js`, { method: "HEAD" });
    assert.equal(res.status, 200);
    assert.equal(await res.text(), "");
  });

  it("rejects writes to page paths", async () => {
    const res = await fetch(`${base}/settings`, { method: "POST" });
    assert.equal(res.status, 405);
  });

  it("sets security headers on the UI", async () => {
    for (const target of ["/", "/assets/app-1.js", "/assets/old.js"]) {
      const res = await fetch(base + target);
      assert.equal(res.headers.get("x-content-type-options"), "nosniff", target);
      assert.match(res.headers.get("content-security-policy"), /frame-ancestors 'none'/, target);
    }
  });

  it("lets only the picker popup load Google's scripts", async () => {
    const picker = await fetch(`${base}/drive-picker.html`);
    assert.equal(picker.status, 200);
    assert.match(picker.headers.get("content-security-policy"), /script-src 'self' https:\/\/apis\.google\.com/);
    assert.match(picker.headers.get("content-security-policy"), /frame-ancestors 'none'/);
    assert.equal(picker.headers.get("referrer-policy"), "strict-origin-when-cross-origin");

    for (const target of ["/", "/storage", "/assets/app-1.js"]) {
      const res = await fetch(base + target);
      assert.doesNotMatch(res.headers.get("content-security-policy"), /google/, target);
      assert.equal(res.headers.get("referrer-policy"), "same-origin", target);
    }
  });

  it("does not serve hidden files or anything outside the root", async () => {
    const port = new URL(base).port;
    for (const target of [
      "/.hidden",
      "/../outside.txt",
      "/%2e%2e/outside.txt",
      "/assets/%2e%2e/%2e%2e/outside.txt",
      "/..%5coutside.txt",
      "/%00",
      "/%E0%A4%A",
    ]) {
      const { body } = await rawGet(port, target);
      assert.ok(!body.includes("outside the root"), target);
      assert.ok(!body.includes("hidden"), target);
    }
  });

  it("proxies API paths and passes the response through", async () => {
    const res = await fetch(`${base}/api/v1/things?page=2`);
    assert.deepEqual(await res.json(), { from: "api" });
    assert.equal(lastApiRequest.url, "/api/v1/things?page=2");
    assert.equal(res.headers.get("set-cookie"), "session=abc; HttpOnly");

    await fetch(`${base}/health`);
    assert.equal(lastApiRequest.url, "/health");
  });

  it("passes API error statuses through unchanged", async () => {
    const res = await fetch(`${base}/api/v1/missing`);
    assert.equal(res.status, 404);
  });

  it("forwards the method, body, cookies, Origin and Host", async () => {
    await fetch(`${base}/api/v1/things`, {
      method: "POST",
      body: "hello",
      headers: { cookie: "session=abc", origin: "https://dextro.example" },
    });
    assert.equal(lastApiRequest.method, "POST");
    assert.equal(lastApiRequest.body, "hello");
    assert.equal(lastApiRequest.headers.cookie, "session=abc");
    assert.equal(lastApiRequest.headers.origin, "https://dextro.example");
    assert.equal(lastApiRequest.headers.host, new URL(base).host);
  });

  it("replaces a client-supplied X-Forwarded-For", async () => {
    await fetch(`${base}/api/v1/things`, { headers: { "x-forwarded-for": "1.2.3.4" } });
    assert.ok(!lastApiRequest.headers["x-forwarded-for"].includes("1.2.3.4"));
  });

  it("does not time out long requests", () => {
    assert.equal(web.requestTimeout, 0);
  });

  it("answers 502 without details when the API is down", async () => {
    const down = http.createServer();
    const downPort = await listen(down);
    await new Promise((r) => down.close(r));

    const app = await createApp({
      root: path.join(dir, "dist"),
      apiUrl: new URL(`http://127.0.0.1:${downPort}`),
      logger: silent,
    });
    const port = await listen(app);
    const res = await fetch(`http://127.0.0.1:${port}/health`);
    const text = await res.text();
    app.closeAllConnections();
    await new Promise((r) => app.close(r));

    assert.equal(res.status, 502);
    assert.equal(JSON.parse(text).error.code, "bad_gateway");
    assert.ok(!text.includes(String(downPort)));
  });

  it("refuses to start without a build", async () => {
    await assert.rejects(createApp({ root: path.join(dir, "nope"), apiUrl: new URL("http://127.0.0.1:1"), logger: silent }));
  });
});
