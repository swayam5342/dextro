// A stand-in for the Tributary server, so the real app can run with no server at all.
// It answers the app's API calls in the browser from made-up data. Nothing is sent
// anywhere and a reload starts again. The routing here follows internal/storage/routing.go.
(function () {
  const GB = 1024 ** 3;
  const MB = 1024 ** 2;
  const CHUNK_MS = 900;
  const realFetch = window.fetch.bind(window);
  const now = () => new Date().toISOString();
  const earlier = (days) => new Date(Date.now() - days * 86400000).toISOString();
  const newId = () => crypto.randomUUID();

  const account = (id, email, other, days) => ({
    id,
    name: `Google Drive (${email})`,
    provider: "google_drive",
    summary: email,
    created_at: earlier(days),
    draining: false,
    other,
  });
  const accounts = [
    account("north", "north.fork@example.com", 6.4 * GB, 90),
    account("south", "south.fork@example.com", 2.1 * GB, 60),
    account("creek", "little.creek@example.com", 0.7 * GB, 30),
  ];
  const LIMIT = 15 * GB;

  const folder = (id, name, parent, days) => ({ id, name, parent_id: parent, trashed_at: null, created_at: earlier(days), updated_at: earlier(days) });
  const folders = [
    folder("survey", "Ganga survey", null, 40),
    folder("work", "Work", null, 30),
    folder("invoices", "Invoices", "work", 20),
  ];

  const file = (id, name, size, type, preview, connection, parent, days) => ({
    id, external: false, name, size, content_type: type, preview, connection_id: connection, folder_id: parent,
    starred: false, trashed_at: null, created_at: earlier(days), updated_at: earlier(days),
  });
  const files = [
    file("rennell-1786", "Rennell, Bengal and Oude 1786.jpg", 41 * MB, "image/jpeg", "image", "north", "survey", 40),
    file("rennell-survey", "Rennell, An Actual Survey.jpg", 44 * MB, "image/jpeg", "image", "south", "survey", 40),
    file("ganga-course", "Course of the Ganges, dry season.jpg", 22 * MB, "image/jpeg", "image", "creek", "survey", 40),
    file("tieffenthaler", "Tieffenthaler, Ganga and Ghaghara 1784.jpg", 39 * MB, "image/jpeg", "image", "creek", "survey", 40),
    file("tour", "Tributary tour.mp4", 2.6 * MB, "video/mp4", "video", "north", null, 12),
    file("budget", "Budget 2026.xlsx", 340 * 1024, "application/vnd.ms-excel", "none", "south", "work", 9),
    file("notes", "Meeting notes.docx", 88 * 1024, "application/msword", "none", "north", "work", 6),
    file("inv-1", "Invoice 1041.pdf", 210 * 1024, "application/octet-stream", "none", "creek", "invoices", 20),
    file("inv-2", "Invoice 1042.pdf", 196 * 1024, "application/octet-stream", "none", "north", "invoices", 18),
    file("backup", "Laptop backup.zip", 1.8 * GB, "application/zip", "none", "south", null, 3),
  ].map((f) => ({ ...f, size: Math.round(f.size) }));

  const state = {
    signedIn: true,
    routing: { mode: "most_space", order: accounts.map((a) => a.id), last: null },
    uploads: [],
    shares: [],
    keys: [],
  };
  const user = {
    id: "demo-user", email: "you@example.com", role: "user", has_password: true, google_linked: false,
    must_change_password: false, created_at: earlier(90),
  };

  const refused = (message) => ({ status: 400, body: { error: { code: "demo", message, request_id: "demo" } } });
  const missing = () => ({ status: 404, body: { error: { code: "not_found", message: "Not found.", request_id: "demo" } } });
  const done = () => ({ status: 204 });
  const ok = (body, status = 200) => ({ status, body });

  const held = (id) => state.uploads.filter((u) => u.connection_id === id && !u.file).reduce((sum, u) => sum + u.size, 0);
  const used = (a) => Math.round(a.other + files.filter((f) => f.connection_id === a.id).reduce((sum, f) => sum + f.size, 0));
  const free = (a) => LIMIT - used(a) - held(a.id);
  const publicAccount = ({ other, ...rest }) => rest;

  function pool() {
    const list = accounts.map((a) => ({ connection: publicAccount(a), available: true, used_bytes: used(a), reserved_bytes: held(a.id), limit_bytes: LIMIT }));
    return {
      picker_enabled: false,
      accounts: list,
      used_bytes: list.reduce((sum, a) => sum + a.used_bytes, 0),
      limit_bytes: LIMIT * accounts.length,
      free_bytes: accounts.filter((a) => !a.draining).reduce((sum, a) => sum + Math.max(free(a), 0), 0),
    };
  }

  function routingOrder() {
    const listed = state.routing.order.filter((id) => accounts.some((a) => a.id === id));
    return [...listed, ...accounts.map((a) => a.id).filter((id) => !listed.includes(id))];
  }
  const routingBody = () => ({ mode: state.routing.mode, order: routingOrder(), last_connection_id: state.routing.last });

  /** Picks an account the way the server does, or says why it can't. */
  function choose(picked, size) {
    if (picked) {
      const chosen = accounts.find((a) => a.id === picked);
      return chosen && free(chosen) >= size ? chosen : null;
    }
    const usable = accounts.filter((a) => !a.draining);
    const { mode, last } = state.routing;
    let tries;
    if (mode === "most_space") {
      tries = [...usable].sort((a, b) => free(b) - free(a));
    } else {
      const order = routingOrder();
      tries = order.map((id) => usable.find((a) => a.id === id)).filter(Boolean);
      if (mode === "round_robin" && last) {
        const at = tries.findIndex((a) => a.id === last);
        tries = [...tries.slice(at + 1), ...tries.slice(0, at + 1)];
      }
    }
    const chosen = tries.find((a) => free(a) >= size);
    if (chosen && mode === "round_robin") {
      state.routing.last = chosen.id;
    }
    return chosen ?? null;
  }

  function contents(folderId) {
    const path = [];
    for (let at = folderId; at; at = folders.find((f) => f.id === at)?.parent_id) {
      const found = folders.find((f) => f.id === at);
      if (found) path.unshift(found);
    }
    return {
      path,
      folders: folders.filter((f) => f.parent_id === (folderId ?? null) && !f.trashed_at),
      files: files.filter((f) => f.folder_id === (folderId ?? null) && !f.trashed_at),
    };
  }

  const live = () => files.filter((f) => !f.trashed_at && !folders.find((d) => d.id === f.folder_id)?.trashed_at);

  function setStar(id, starred) {
    const found = files.find((f) => f.id === id);
    if (!found) return missing();
    found.starred = starred;
    return done();
  }

  function usage() {
    const total = { photo_bytes: 0, video_bytes: 0, document_bytes: 0 };
    for (const f of files) {
      if (f.content_type.startsWith("image/")) total.photo_bytes += f.size;
      else if (f.content_type.startsWith("video/")) total.video_bytes += f.size;
      else if (f.content_type !== "application/zip") total.document_bytes += f.size;
    }
    return total;
  }

  function storeUpload(upload) {
    const stored = file(newId(), upload.name, upload.size, upload.content_type || "application/octet-stream", "none", upload.connection_id, upload.folder_id ?? null, 0);
    files.push(stored);
    return stored;
  }

  /** Empties an account into whichever others have the most room. */
  function moveAll(from) {
    for (const f of files.filter((item) => item.connection_id === from)) {
      const to = accounts.filter((a) => a.id !== from && !a.draining).sort((a, b) => free(b) - free(a))[0];
      if (to) f.connection_id = to.id;
    }
  }

  const removeFile = (id) => {
    const at = files.findIndex((f) => f.id === id);
    if (at >= 0) files.splice(at, 1);
  };
  function removeFolder(id) {
    for (const child of folders.filter((f) => f.parent_id === id)) removeFolder(child.id);
    for (const f of files.filter((item) => item.folder_id === id)) removeFile(f.id);
    folders.splice(folders.findIndex((f) => f.id === id), 1);
  }

  /** Every route the app calls. Each entry is [method, pattern, handler]. */
  const routes = [
    ["GET", /^\/api\/v1\/auth\/config$/, () => ok({ registration_open: false, google_enabled: true })],
    ["GET", /^\/api\/v1\/auth\/me$/, () => (state.signedIn ? ok(user) : { status: 401, body: { error: { code: "unauthorized", message: "Sign in to continue.", request_id: "demo" } } })],
    ["POST", /^\/api\/v1\/auth\/login$/, () => { state.signedIn = true; return ok(user); }],
    ["POST", /^\/api\/v1\/auth\/logout$/, () => { state.signedIn = false; return done(); }],
    ["POST", /^\/api\/v1\/auth\/password$/, () => done()],
    ["POST", /^\/api\/v1\/auth\/google\/link$/, () => refused("Linking a Google account is off in this demo.")],
    ["GET", /^\/api\/v1\/auth\/sessions$/, () => ok([{ id: "this", current: true, user_agent: navigator.userAgent, ip: "127.0.0.1", created_at: earlier(1), last_seen_at: now(), expires_at: earlier(-30) }])],
    ["DELETE", /^\/api\/v1\/auth\/sessions\/[^/]+$/, () => done()],
    ["GET", /^\/api\/v1\/auth\/api-keys$/, () => ok(state.keys)],
    ["POST", /^\/api\/v1\/auth\/api-keys$/, (_, body) => {
      const key = { id: newId(), name: body.name, prefix: "dxt_demo", scopes: body.scopes, created_at: now(), last_used_at: null };
      state.keys.push(key);
      return ok({ key, token: "dxt_demo_this_key_only_works_in_the_demo" }, 201);
    }],
    ["DELETE", /^\/api\/v1\/auth\/api-keys\/([^/]+)$/, ([, id]) => { state.keys = state.keys.filter((k) => k.id !== id); return done(); }],

    ["GET", /^\/api\/v1\/storage\/connections$/, () => ok(accounts.map(publicAccount))],
    ["GET", /^\/api\/v1\/storage\/pool$/, () => ok(pool())],
    ["GET", /^\/api\/v1\/storage\/routing$/, () => ok(routingBody())],
    ["PUT", /^\/api\/v1\/storage\/routing$/, (_, body) => { state.routing.mode = body.mode; state.routing.order = body.order; return ok(routingBody()); }],
    ["POST", /^\/api\/v1\/storage\/google\/connect$/, () => refused("Linking a real Google account is off in this demo.")],
    ["POST", /^\/api\/v1\/storage\/connections\/[^/]+\/sync$/, () => ok({ added: 0, removed: 0 })],
    ["POST", /^\/api\/v1\/storage\/connections\/[^/]+\/test$/, () => done()],
    ["DELETE", /^\/api\/v1\/storage\/connections\/[^/]+$/, () => refused("Accounts can't be removed in this demo.")],
    ["POST", /^\/api\/v1\/storage\/connections\/([^/]+)\/empty$/, ([, id]) => {
      const held = files.filter((f) => f.connection_id === id).length;
      moveAll(id);
      accounts.find((a) => a.id === id).draining = true;
      return ok({ queued: held, skipped: 0 });
    }],
    ["DELETE", /^\/api\/v1\/storage\/connections\/([^/]+)\/empty$/, ([, id]) => { accounts.find((a) => a.id === id).draining = false; return done(); }],

    ["GET", /^\/api\/v1\/usage$/, () => ok(usage())],
    ["GET", /^\/api\/v1\/recent$/, () => ok({ files: live().sort((a, b) => b.created_at.localeCompare(a.created_at)).slice(0, 50) })],
    ["GET", /^\/api\/v1\/starred$/, () => ok({ files: live().filter((f) => f.starred) })],
    ["PUT", /^\/api\/v1\/files\/([^/]+)\/star$/, ([, id]) => setStar(id, true)],
    ["DELETE", /^\/api\/v1\/files\/([^/]+)\/star$/, ([, id]) => setStar(id, false)],
    ["GET", /^\/api\/v1\/files$/, (_, __, query) => ok(contents(query.get("folder_id")))],
    ["PATCH", /^\/api\/v1\/files\/([^/]+)$/, ([, id], body) => {
      const found = files.find((f) => f.id === id);
      if (!found) return missing();
      found.name = body.name;
      found.updated_at = now();
      return ok(found);
    }],
    ["DELETE", /^\/api\/v1\/files\/([^/]+)$/, ([, id]) => { removeFile(id); return done(); }],
    ["POST", /^\/api\/v1\/files\/([^/]+)\/move$/, ([, id], body) => {
      const found = files.find((f) => f.id === id);
      if (!found) return missing();
      found.folder_id = body.folder_id;
      return ok(found);
    }],
    ["POST", /^\/api\/v1\/files\/([^/]+)\/trash$/, ([, id]) => {
      const found = files.find((f) => f.id === id);
      if (found) found.trashed_at = now();
      state.shares = state.shares.filter((s) => s.file_id !== id);
      return done();
    }],
    ["POST", /^\/api\/v1\/files\/([^/]+)\/restore$/, ([, id]) => {
      const found = files.find((f) => f.id === id);
      if (found) found.trashed_at = null;
      return done();
    }],
    ["PUT", /^\/api\/v1\/files\/([^/]+)\/share$/, ([, id], body) => {
      const found = files.find((f) => f.id === id);
      if (!found) return missing();
      state.shares = state.shares.filter((s) => s.file_id !== id);
      const share = { file_id: id, name: found.name, role: body.role, url: `https://drive.google.com/file/d/made-up-${id}/view`, expires_at: body.expires_at ?? null, created_at: now() };
      state.shares.push(share);
      return ok(share);
    }],
    ["DELETE", /^\/api\/v1\/files\/([^/]+)\/share$/, ([, id]) => { state.shares = state.shares.filter((s) => s.file_id !== id); return done(); }],
    ["GET", /^\/api\/v1\/shares$/, () => ok({ enabled: true, shares: state.shares })],

    ["POST", /^\/api\/v1\/folders$/, (_, body) => {
      const made = folder(newId(), body.name, body.parent_id ?? null, 0);
      folders.push(made);
      return ok(made, 201);
    }],
    ["PATCH", /^\/api\/v1\/folders\/([^/]+)$/, ([, id], body) => {
      const found = folders.find((f) => f.id === id);
      if (!found) return missing();
      found.name = body.name;
      return ok(found);
    }],
    ["DELETE", /^\/api\/v1\/folders\/([^/]+)$/, ([, id]) => { removeFolder(id); return done(); }],
    ["POST", /^\/api\/v1\/folders\/([^/]+)\/move$/, ([, id], body) => {
      const found = folders.find((f) => f.id === id);
      if (!found) return missing();
      found.parent_id = body.parent_id;
      return ok(found);
    }],
    ["POST", /^\/api\/v1\/folders\/([^/]+)\/trash$/, ([, id]) => {
      const found = folders.find((f) => f.id === id);
      if (found) found.trashed_at = now();
      return done();
    }],
    ["POST", /^\/api\/v1\/folders\/([^/]+)\/restore$/, ([, id]) => {
      const found = folders.find((f) => f.id === id);
      if (found) found.trashed_at = null;
      return done();
    }],

    ["GET", /^\/api\/v1\/trash$/, () => ok({ folders: folders.filter((f) => f.trashed_at), files: files.filter((f) => f.trashed_at) })],
    ["DELETE", /^\/api\/v1\/trash$/, () => {
      for (const f of files.filter((item) => item.trashed_at)) removeFile(f.id);
      for (const f of folders.filter((item) => item.trashed_at)) removeFolder(f.id);
      return done();
    }],
    ["GET", /^\/api\/v1\/search$/, (_, __, query) => {
      const q = (query.get("q") ?? "").toLowerCase();
      const hit = (item) => !item.trashed_at && item.name.toLowerCase().includes(q);
      return ok({ folders: folders.filter(hit), files: files.filter(hit) });
    }],
    ["GET", /^\/api\/v1\/zip$/, () => refused("Zip downloads are off in this demo.")],

    ["GET", /^\/api\/v1\/transfers$/, () => ok({
      transfers: [], waiting: 0, waiting_bytes: 0, failed: 0, removing: 0,
      accounts: accounts.map((a) => ({ connection_id: a.id, files: files.filter((f) => f.connection_id === a.id).length, external_files: 0 })),
    })],
    ["POST", /^\/api\/v1\/transfers$/, (_, body) => {
      for (const id of body.file_ids) {
        const found = files.find((f) => f.id === id);
        if (found) found.connection_id = body.target_connection_id;
      }
      return ok({ queued: body.file_ids.length, skipped: 0 });
    }],

    ["GET", /^\/api\/v1\/uploads$/, () => ok([])],
    ["POST", /^\/api\/v1\/uploads$/, (_, body) => {
      const chosen = choose(body.connection_id, body.size);
      if (!chosen) return { status: 507, body: { error: { code: "storage_full", message: "No account has room for this file.", request_id: "demo" } } };
      const upload = {
        id: newId(), connection_id: chosen.id, folder_id: body.folder_id ?? null, name: body.name, size: body.size,
        content_type: body.content_type, received: 0, last_modified: body.last_modified ?? Date.now(), created_at: now(),
      };
      state.uploads.push(upload);
      return ok(upload, 201);
    }],
    ["GET", /^\/api\/v1\/uploads\/([^/]+)$/, ([, id]) => {
      const upload = state.uploads.find((u) => u.id === id);
      return upload ? ok(upload) : missing();
    }],
    ["DELETE", /^\/api\/v1\/uploads\/([^/]+)$/, ([, id]) => { state.uploads = state.uploads.filter((u) => u.id !== id); return done(); }],
  ];

  function answer(method, path, query, body) {
    for (const [verb, pattern, handler] of routes) {
      const match = verb === method && path.match(pattern);
      if (match) return handler(match, body, query);
    }
    return missing();
  }

  window.fetch = async function (input, init) {
    const request = input instanceof Request ? input : new Request(input, init);
    const url = new URL(request.url, location.href);
    if (url.origin !== location.origin || !url.pathname.startsWith("/api/")) {
      return realFetch(input, init);
    }
    const text = await request.clone().text();
    const result = answer(request.method, url.pathname, url.searchParams, text ? JSON.parse(text) : undefined);
    if (result.status === 204) {
      return new Response(null, { status: 204 });
    }
    return new Response(JSON.stringify(result.body), { status: result.status, headers: { "Content-Type": "application/json" } });
  };

  /** Uploads use XHR for its progress events. Nothing is sent: the bytes stay in the browser. */
  class DemoRequest {
    upload = {};
    status = 0;
    responseText = "";
    open(method, url) {
      this.method = method;
      this.url = new URL(url, location.href);
    }
    setRequestHeader(name, value) {
      if (name.toLowerCase() === "content-type") this.contentType = value;
    }
    send(body) {
      const size = body?.size ?? 0;
      const started = performance.now();
      this.timer = setInterval(() => {
        const part = Math.min((performance.now() - started) / CHUNK_MS, 1);
        this.upload.onprogress?.({ loaded: Math.round(size * part) });
        if (part === 1) {
          clearInterval(this.timer);
          this.finish(size);
        }
      }, 60);
    }
    finish(size) {
      const chunk = this.url.pathname.match(/^\/api\/v1\/uploads\/([^/]+)\/chunk$/);
      let result;
      if (chunk) {
        const upload = state.uploads.find((u) => u.id === chunk[1]);
        upload.received = Number(this.url.searchParams.get("offset")) + size;
        if (upload.received >= upload.size) {
          upload.file = storeUpload(upload);
        }
        result = ok(upload);
      } else {
        const query = this.url.searchParams;
        const chosen = choose(query.get("connection_id"), size);
        result = chosen
          ? ok(storeUpload({ name: query.get("name"), size, content_type: this.contentType, connection_id: chosen.id, folder_id: query.get("folder_id") }), 201)
          : ok({ error: { code: "storage_full", message: "No account has room for this file.", request_id: "demo" } }, 507);
      }
      this.status = result.status;
      this.responseText = JSON.stringify(result.body);
      this.onload?.();
    }
    abort() {
      clearInterval(this.timer);
      this.onabort?.();
    }
  }
  window.XMLHttpRequest = DemoRequest;

  /**
   * Says what this is, on every page of the demo. It takes its own strip at the top rather
   * than floating, because anything floating ends up over a toast or the upload panel. It
   * sits under dialogs, so it can't cover the viewer's buttons.
   */
  addEventListener("DOMContentLoaded", () => {
    const style = document.createElement("style");
    style.textContent = "body{box-sizing:border-box;padding-top:32px}#root{height:100%}";
    const bar = document.createElement("div");
    bar.setAttribute("role", "note");
    bar.style.cssText = "position:fixed;inset:0 0 auto 0;height:32px;z-index:40;display:flex;gap:12px;align-items:center;justify-content:center;white-space:nowrap;background:#0d3a55;color:#fff;font:500 13px 'Instrument Sans',system-ui,sans-serif";
    const text = document.createElement("span");
    text.textContent = "Demo with made-up files";
    const back = document.createElement("a");
    back.href = "/";
    back.textContent = "Back to the site";
    back.style.cssText = "color:#fff;text-underline-offset:2px";
    bar.append(text, back);
    document.head.append(style);
    document.body.append(bar);
  });
})();
