// Builds the real app into public/demo, with demo-api.js answering its API calls.
// Run this when the app changes: pnpm build:demo
import { execFileSync } from "node:child_process";
import { cpSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const site = join(dirname(fileURLToPath(import.meta.url)), "..");
const app = join(site, "..", "web");
const out = join(site, "public", "demo");

rmSync(out, { recursive: true, force: true });
execFileSync("pnpm", ["exec", "vite", "build", "--base=/demo/", "--outDir", out, "--emptyOutDir"], {
  cwd: app,
  stdio: "inherit",
  shell: process.platform === "win32",
});
// The picker has no use here: it needs a real Google account.
rmSync(join(out, "drive-picker.html"), { force: true });
rmSync(join(out, "drive-picker.js"), { force: true });

cpSync(join(site, "demo-api", "demo-api.js"), join(out, "demo-api.js"));

const page = join(out, "index.html");
const html = readFileSync(page, "utf8");
if (!html.includes("<head>")) {
  throw new Error("no <head> in the built page");
}
// What a shared link to the demo shows.
const preview = `
    <meta name="description" content="Try Tributary in your browser with made-up files. No sign-up needed." />
    <meta property="og:title" content="Try Tributary: many Google Drives, one pool" />
    <meta property="og:description" content="The real app, running in your browser with made-up files. No sign-up needed." />
    <meta property="og:url" content="https://tributary.swayam.li/demo" />
    <meta property="og:image" content="https://tributary.swayam.li/og.jpg" />
    <meta name="twitter:card" content="summary_large_image" />`;
// A plain script runs before the app's module, so the app only ever sees the stand-in.
writeFileSync(page, html.replace("<head>", `<head>\n    <script src="/demo/demo-api.js"></script>${preview}`));
console.log("demo built");
