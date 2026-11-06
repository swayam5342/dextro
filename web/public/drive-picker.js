// Runs in its own popup, because Google's picker needs Google's scripts and an
// access token in the browser. Keeping them here leaves the main app's
// Content-Security-Policy strict. The token stays in this script's memory, and goes
// away when the popup closes.
"use strict";

const status = document.getElementById("status");
const connection = new URLSearchParams(location.search).get("connection");

function show(text) {
  status.textContent = text;
}

async function post(path, body) {
  const response = await fetch(`/api/v1/storage/connections/${encodeURIComponent(connection)}/${path}`, {
    method: "POST",
    headers: body ? { "Content-Type": "application/json" } : undefined,
    body: body ? JSON.stringify(body) : undefined,
  });
  const result = await response.json().catch(() => undefined);
  if (!response.ok) {
    throw new Error(result?.error?.message ?? "Something went wrong. Close this window and try again.");
  }
  return result;
}

function loadPicker() {
  return new Promise((resolve, reject) => {
    const script = document.createElement("script");
    script.src = "https://apis.google.com/js/api.js";
    script.onerror = () => reject(new Error("Google's picker couldn't be loaded."));
    script.onload = () => gapi.load("picker", { callback: resolve, onerror: () => reject(new Error("Google's picker couldn't be loaded.")) });
    document.head.append(script);
  });
}

async function handlePicked(data) {
  const { Action, Response } = google.picker;
  if (data[Response.ACTION] === Action.CANCEL) {
    window.close();
    return;
  }
  if (data[Response.ACTION] !== Action.PICKED) {
    return;
  }
  show("Adding your files…");
  try {
    const result = await post("import", { file_ids: data[Response.DOCUMENTS].map((doc) => doc.id) });
    window.opener?.postMessage({ type: "dextro-files-added", ...result }, location.origin);
    window.close();
  } catch (error) {
    show(error.message);
  }
}

async function start() {
  if (!connection) {
    show("No account was chosen. Close this window and try again.");
    return;
  }
  const [access] = await Promise.all([post("picker"), loadPicker()]);
  const view = new google.picker.DocsView()
    .setIncludeFolders(true)
    .setSelectFolderEnabled(false)
    .setMode(google.picker.DocsViewMode.LIST);
  new google.picker.PickerBuilder()
    .addView(view)
    .enableFeature(google.picker.Feature.MULTISELECT_ENABLED)
    .setOAuthToken(access.access_token)
    .setDeveloperKey(access.developer_key)
    .setAppId(access.app_id)
    .setOrigin(location.origin)
    .setCallback((data) => void handlePicked(data))
    .build()
    .setVisible(true);
  show("");
}

start().catch((error) => show(error.message));
