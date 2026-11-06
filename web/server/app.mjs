// The production web server. It serves the built frontend and proxies API calls
// to the Go server, so the browser sees a single origin: no CORS, and session
// cookies stay first-party.

import { createReadStream } from "node:fs";
import { readFile, stat } from "node:fs/promises";
import http from "node:http";
import path from "node:path";
import { pipeline } from "node:stream";

const CONTENT_SECURITY_POLICY = [
  "default-src 'self'",
  "img-src 'self' data: blob:",
  "object-src 'none'",
  "base-uri 'self'",
  "form-action 'self'",
  "frame-ancestors 'none'",
].join("; ");

// The popup that runs Google's picker. Its script and frames come from Google, so it
// gets its own policy and the rest of the app keeps the strict one above. Referrers
// are on here because Google checks them against the API key's allowed sites.
const PICKER_PATH = "/drive-picker.html";
const PICKER_REFERRER_POLICY = "strict-origin-when-cross-origin";
const PICKER_CONTENT_SECURITY_POLICY = [
  "default-src 'self'",
  "script-src 'self' https://apis.google.com",
  "style-src 'self' 'unsafe-inline' https://*.google.com https://*.gstatic.com",
  "img-src 'self' data: https:",
  "font-src https://fonts.gstatic.com",
  "frame-src https://docs.google.com https://drive.google.com https://accounts.google.com https://content.googleapis.com",
  "connect-src 'self' https://*.googleapis.com https://*.google.com",
  "object-src 'none'",
  "base-uri 'none'",
  "form-action 'none'",
  "frame-ancestors 'none'",
].join("; ");

const CONTENT_TYPES = {
  ".css": "text/css; charset=utf-8",
  ".html": "text/html; charset=utf-8",
  ".ico": "image/x-icon",
  ".js": "text/javascript; charset=utf-8",
  ".json": "application/json",
  ".map": "application/json",
  ".png": "image/png",
  ".svg": "image/svg+xml",
  ".txt": "text/plain; charset=utf-8",
  ".woff2": "font/woff2",
};

// Headers that describe one network hop and must not be forwarded.
const HOP_BY_HOP_HEADERS = [
  "connection",
  "keep-alive",
  "proxy-authenticate",
  "proxy-authorization",
  "proxy-connection",
  "te",
  "trailer",
  "upgrade",
];

function isApiPath(pathname) {
  return pathname === "/health" || pathname === "/api" || pathname.startsWith("/api/");
}

function withoutHopByHop(headers) {
  const result = { ...headers };
  for (const name of HOP_BY_HOP_HEADERS) {
    delete result[name];
  }
  return result;
}

function sendError(res, status, code, message) {
  res.writeHead(status, { "Content-Type": "application/json" });
  res.end(JSON.stringify({ error: { code, message, request_id: "" } }));
}

/** Streams the request to the API and the response back, buffering neither. */
function proxy(req, res, apiUrl, logger) {
  const headers = withoutHopByHop(req.headers);
  // Overwritten, never appended to, so a client can't forge its own address.
  headers["x-forwarded-for"] = req.socket.remoteAddress ?? "";

  const upstream = http.request(
    { hostname: apiUrl.hostname, port: apiUrl.port, method: req.method, path: req.url, headers },
    (upstreamRes) => {
      res.writeHead(upstreamRes.statusCode ?? 502, withoutHopByHop(upstreamRes.headers));
      pipeline(upstreamRes, res, () => {});
    },
  );
  upstream.on("error", (error) => {
    logger.error(`proxy ${req.method} ${new URL(req.url, "http://x").pathname}: ${error.message}`);
    if (res.headersSent) {
      res.destroy();
    } else {
      sendError(res, 502, "bad_gateway", "The server is unavailable.");
    }
  });
  // pipeline tears down the upstream request if the client disconnects.
  pipeline(req, upstream, () => {});
}

/** Maps a URL path to a file inside root, or null if it must not be served. */
function resolveFile(root, pathname) {
  let decoded;
  try {
    decoded = decodeURIComponent(pathname);
  } catch {
    return null;
  }
  if (decoded.includes("\0") || decoded.includes("\\")) {
    return null;
  }
  const segments = decoded.split("/").filter(Boolean);
  if (segments.some((segment) => segment.startsWith("."))) {
    return null;
  }
  const file = path.join(root, ...segments);
  return file.startsWith(root + path.sep) ? file : null;
}

async function isFile(file) {
  try {
    return (await stat(file)).isFile();
  } catch {
    return false;
  }
}

function sendFile(req, res, file, cacheControl) {
  const headers = { "Content-Type": CONTENT_TYPES[path.extname(file)] ?? "application/octet-stream" };
  if (cacheControl) {
    headers["Cache-Control"] = cacheControl;
  }
  res.writeHead(200, headers);
  if (req.method === "HEAD") {
    res.end();
    return;
  }
  pipeline(createReadStream(file), res, () => {});
}

async function serveStatic(req, res, root, index, pathname) {
  if (req.method !== "GET" && req.method !== "HEAD") {
    sendError(res, 405, "method_not_allowed", "Method not allowed.");
    return;
  }

  const file = pathname === "/index.html" ? null : resolveFile(root, pathname);
  if (file && (await isFile(file))) {
    // Vite puts a content hash in the names of files under assets/.
    const isHashed = pathname.startsWith("/assets/");
    sendFile(req, res, file, isHashed ? "public, max-age=31536000, immutable" : undefined);
    return;
  }

  // Stale asset links need a real 404; everything else is a client-side route.
  if (pathname.startsWith("/assets/")) {
    sendError(res, 404, "not_found", "Not found.");
    return;
  }
  res.writeHead(200, { "Content-Type": CONTENT_TYPES[".html"], "Cache-Control": "no-cache" });
  res.end(req.method === "HEAD" ? undefined : index);
}

/**
 * Creates the web server. It fails if root has no index.html, so a missing
 * build stops the process at startup.
 */
export async function createApp({ root, apiUrl, logger = console }) {
  const resolvedRoot = path.resolve(root);
  const index = await readFile(path.join(resolvedRoot, "index.html"));

  const server = http.createServer((req, res) => {
    const { pathname } = new URL(req.url, "http://x");
    if (isApiPath(pathname)) {
      proxy(req, res, apiUrl, logger);
      return;
    }

    const isPicker = pathname === PICKER_PATH;
    res.setHeader("Content-Security-Policy", isPicker ? PICKER_CONTENT_SECURITY_POLICY : CONTENT_SECURITY_POLICY);
    res.setHeader("X-Content-Type-Options", "nosniff");
    res.setHeader("Referrer-Policy", isPicker ? PICKER_REFERRER_POLICY : "same-origin");
    serveStatic(req, res, resolvedRoot, index, pathname).catch((error) => {
      logger.error(`serve ${pathname}: ${error.message}`);
      res.destroy();
    });
  });

  // Node's default would cut off any upload that takes over five minutes.
  server.requestTimeout = 0;
  return server;
}
