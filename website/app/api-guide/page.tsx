import type { Metadata } from "next";
import Link from "next/link";

import { CodeTabs } from "@/components/code-tabs";
import { DocsShell, type DocGroup } from "@/components/docs-shell";
import { read, samples } from "@/lib/examples";

export const metadata: Metadata = {
  title: "API guide",
  description: "What you can do with a Tributary API key, with examples in curl, Python, JavaScript and Go.",
};

const GROUPS: DocGroup[] = [
  {
    title: "Get started",
    items: [
      ["ideas", "What you can build"],
      ["key", "Get a key"],
      ["first", "Your first request"],
    ],
  },
  {
    title: "Examples",
    items: [
      ["upload", "Upload a file"],
      ["resume", "Resume a large upload"],
      ["download", "Download a file"],
      ["search", "Search"],
      ["organize", "Folders and moving"],
      ["trash", "Trash and restore"],
      ["backup", "Nightly backup script"],
    ],
  },
  {
    title: "Reference",
    items: [
      ["reach", "What a key can do"],
      ["errors", "Errors"],
      ["safe", "Keeping a key safe"],
    ],
  },
];

const READ = [
  ["List a folder", "GET /api/v1/files"],
  ["Get a file's details", "GET /api/v1/files/{id}"],
  ["Download a file", "GET /api/v1/files/{id}/content"],
  ["Get a thumbnail", "GET /api/v1/files/{id}/thumbnail"],
  ["List the trash", "GET /api/v1/trash"],
  ["Search", "GET /api/v1/search?q=..."],
];

const WRITE = [
  ["Upload a file", "POST /api/v1/files?name=..."],
  ["Rename a file", "PATCH /api/v1/files/{id}"],
  ["Move a file", "POST /api/v1/files/{id}/move"],
  ["Trash a file", "POST /api/v1/files/{id}/trash"],
  ["Restore a file", "POST /api/v1/files/{id}/restore"],
  ["Make a folder", "POST /api/v1/folders"],
  ["Rename a folder", "PATCH /api/v1/folders/{id}"],
  ["Move a folder", "POST /api/v1/folders/{id}/move"],
  ["Trash a folder", "POST /api/v1/folders/{id}/trash"],
  ["Restore a folder", "POST /api/v1/folders/{id}/restore"],
  ["List unfinished uploads", "GET /api/v1/uploads"],
  ["Start a resumable upload", "POST /api/v1/uploads"],
  ["See how far an upload got", "GET /api/v1/uploads/{id}"],
  ["Send a piece of an upload", "PUT /api/v1/uploads/{id}/chunk?offset=..."],
  ["Cancel an upload", "DELETE /api/v1/uploads/{id}"],
];

const ERRORS = [
  ["401", "unauthorized", "The key is missing, mistyped or revoked."],
  ["403", "forbidden", "The key doesn't have that scope, or keys can't do that at all."],
  ["403", "password_change_required", "The admin set this person's password. They need to sign in and change it."],
  ["404", "not_found", "That file or folder doesn't exist, or isn't yours."],
  ["400", "invalid_name", "The name is empty or isn't allowed."],
  ["400", "invalid_upload", "An upload needs ?name= in its address."],
  ["409", "no_storage", "No Google account is linked yet."],
  ["507", "storage_full", "No linked account has room. A file has to fit in one account."],
  ["413", "file_too_large", "The file is over the limit the admin set."],
  ["409", "offset_mismatch", "A piece didn't start where the upload is. Ask for the upload and carry on from received."],
  ["410", "upload_expired", "The upload sat idle for too long. Start it again."],
  ["502", "provider_unavailable", "Google couldn't be reached. Try again."],
];

export default function ApiGuide() {
  return (
    <DocsShell current="/api-guide" title="API guide" lead="Use your Tributary from your own scripts and apps with an API key." groups={GROUPS} next={{ href: "/self-host", label: "Self-hosting" }}>
    <section>
      <h2 id="ideas">What you can build</h2>
      <ul className="plain">
        <li>A nightly backup of a folder on your laptop or server.</li>
        <li>A script that files invoices, scans or exports into the right folder as they arrive.</li>
        <li>A website or bot that pulls photos and documents out of your pool.</li>
        <li>A camera or phone workflow that uploads as soon as something new appears.</li>
        <li>A clean-up job that finds old files and moves them to the trash.</li>
      </ul>
      <p>Files you add this way appear in the app like any others, and uploads follow your upload routing.</p>
    </section>

    <section>
      <h2 id="key">Get a key</h2>
      <ol className="steps">
        <li>Sign in to your Tributary and open <b>Account</b>.</li>
        <li>Under <b>API keys</b>, name the key after what will use it.</li>
        <li>Choose its scopes: <code>files:read</code> to look, <code>files:write</code> to change things. Give it only what it needs.</li>
        <li>Press <b>Create key</b> and copy it. It starts with <code>dxt_</code>, and it's shown once.</li>
      </ol>
      <p>Keep the key and your server's address in environment variables. The examples here expect these two:</p>
      <CodeTabs samples={[{ label: "Shell", code: 'export TRIBUTARY_URL="https://files.example.com"\nexport TRIBUTARY_KEY="dxt_..."' }]} />
    </section>

    <section>
      <h2 id="first">Your first request</h2>
      <p>
        Every request carries the key in an <code>Authorization: Bearer</code> header. This lists the top level of your files:
        the folders and files in it, each with an <code>id</code> you can use in other requests.
      </p>
      <CodeTabs samples={samples("list")} />
      <p>Each file comes back like this. Sizes are in bytes, and <code>folder_id</code> is null at the top level.</p>
      <CodeTabs
        samples={[
          {
            label: "JSON",
            code: `{
  "id": "6f1c2a70-3b1e-4c58-9a44-0d2f6f1f7c11",
  "name": "report.pdf",
  "size": 482113,
  "content_type": "application/pdf",
  "preview": "pdf",
  "folder_id": null,
  "connection_id": "1b9e4d02-77ac-4a0e-8e1f-5a3c9b2d6e40",
  "external": false,
  "starred": false,
  "trashed_at": null,
  "created_at": "2026-03-02T09:14:07Z",
  "updated_at": "2026-03-02T09:14:07Z"
}`,
          },
        ]}
      />
    </section>

    <section>
      <h2 id="upload">Upload a file</h2>
      <p>
        Send the file as the request body, with its name in the address. Add <code>folder_id</code> to put it in a folder, or{" "}
        <code>connection_id</code> to choose an account yourself. Left out, your upload routing chooses. The answer is the new file.
      </p>
      <CodeTabs samples={samples("upload")} />
      <p>
        If the connection drops, this kind of upload has to start again. Names don't have to be unique, so uploading twice gives you two
        files.
      </p>
    </section>

    <section>
      <h2 id="resume">Resume a large upload</h2>
      <p>
        For a large file, send it in pieces, so a dropped connection only costs you one piece. Start an upload, then send each piece with
        its <code>offset</code>. The answer's <code>received</code> says where the next piece has to start, which can be less than you
        sent, and <code>file</code> appears when it's finished. Every piece but the last must be a multiple of 256 KiB, and at most 64 MiB.
      </p>
      <p>
        To pick up after a problem, ask for the upload by its ID and carry on from <code>received</code>. Space for the whole file is held
        while it's unfinished, and an upload left idle for 24 hours is cleared away. This needs the <code>files:write</code> scope.
      </p>
      <CodeTabs samples={samples("resumable")} />
    </section>

    <section>
      <h2 id="download">Download a file</h2>
      <p>Use the file's <code>id</code>. Downloads support <code>Range</code> requests, so you can fetch part of a large file.</p>
      <CodeTabs samples={samples("download")} />
    </section>

    <section>
      <h2 id="search">Search</h2>
      <p>Search finds files and folders by name, up to 100 of each. Anything in the trash is left out.</p>
      <CodeTabs samples={samples("search")} />
    </section>

    <section>
      <h2 id="organize">Folders and moving</h2>
      <p>
        Moving here means into another folder. Moving a file to a different Google account is only in the app. To move something to
        the top level, send <code>null</code> as the folder.
      </p>
      <CodeTabs samples={samples("organize")} />
    </section>

    <section>
      <h2 id="trash">Trash and restore</h2>
      <p>
        A key can move things to the trash and bring them back, but it can't delete anything for good. Only a signed-in person can
        do that, so a leaked key can't destroy your files. Trashing a file ends its public Google link if it has one, and
        restoring it doesn't bring the link back.
      </p>
      <CodeTabs samples={samples("trash")} />
    </section>

    <section>
      <h2 id="backup">Nightly backup script</h2>
      <p>
        This backs up a folder and skips what's already there, so it's safe to run every night. It needs a key with both scopes,
        and Python's <code>requests</code> package.
      </p>
      <CodeTabs samples={[{ label: "backup.py", code: read("backup.py") }]} />
      <p>To run it at 2am every day, add this line with <code>crontab -e</code>:</p>
      <CodeTabs samples={[{ label: "cron", code: '0 2 * * * TRIBUTARY_URL=https://files.example.com TRIBUTARY_KEY=dxt_... python3 /home/you/backup.py /home/you/Documents "Laptop backup"' }]} />
    </section>

    <section>
      <h2 id="reach">What a key can do</h2>
      <p>A key can make these requests, if it has the scope shown.</p>
      <div className="table-wrap">
        <table className="table">
          <thead>
            <tr>
              <th scope="col">What</th>
              <th scope="col">Request</th>
              <th scope="col">Scope</th>
            </tr>
          </thead>
          <tbody>
            {[...READ.map((row) => [...row, "files:read"]), ...WRITE.map((row) => [...row, "files:write"])].map(([what, route, scope]) => (
              <tr key={route}>
                <td>{what}</td>
                <td>
                  <code>{route}</code>
                </td>
                <td>
                  <code>{scope}</code>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      <p>Everything else is closed to keys, and that includes anything new until it's added to this list.</p>
      <h3>Only a signed-in person can</h3>
      <ul className="plain">
        <li>Delete anything permanently, or empty the trash.</li>
        <li>Make or change public Google links.</li>
        <li>Move files between Google accounts, or link and remove accounts.</li>
        <li>Download a zip.</li>
        <li>Change upload routing, keys, passwords or server settings.</li>
      </ul>
    </section>

    <section>
      <h2 id="errors">Errors</h2>
      <p>Errors have a status, a <code>code</code> your program can check, and a <code>message</code> you can show to a person.</p>
      <CodeTabs samples={[{ label: "JSON", code: `{\n  "error": {\n    "code": "storage_full",\n    "message": "No linked account has room for this file.",\n    "request_id": "U2YB7KRXTUXXOKHA"\n  }\n}` }]} />
      <div className="table-wrap">
        <table className="table">
          <thead>
            <tr>
              <th scope="col">Status</th>
              <th scope="col">Code</th>
              <th scope="col">What it means</th>
            </tr>
          </thead>
          <tbody>
            {ERRORS.map(([status, code, meaning]) => (
              <tr key={code}>
                <td>{status}</td>
                <td>
                  <code>{code}</code>
                </td>
                <td>{meaning}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      <p>If you ask the admin for help, give them the <code>request_id</code>. It finds the full details in the server's logs.</p>
    </section>

    <section>
      <h2 id="safe">Keeping a key safe</h2>
      <ul className="plain">
        <li>Use a key from a server or script, never from a web page. Anyone can read a page's code, and Tributary doesn't allow calls from other sites' pages.</li>
        <li>Make one key per job, so you can revoke one without touching the rest.</li>
        <li>Give each key the fewest scopes it needs. A key that only reads can't change anything.</li>
        <li>Always use your server's <code>https://</code> address, so the key isn't sent in the clear.</li>
        <li>If a key leaks, revoke it under <b>Account</b>. It stops working straight away.</li>
      </ul>
      <p>
        Don't have a server yet? <Link href="/self-host">Set one up</Link>, or <a href="/demo/">try the demo</a> first.
      </p>
    </section>
    </DocsShell>
  );
}
