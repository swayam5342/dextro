import { readFileSync } from "node:fs";
import { join } from "node:path";

import type { Metadata } from "next";
import Link from "next/link";

import { CodeTabs } from "@/components/code-tabs";
import { DocsShell, type DocGroup } from "@/components/docs-shell";
import { GITHUB } from "@/components/site";

export const metadata: Metadata = {
  title: "Self-host",
  description: "Run Tributary on your own machine with Docker.",
};

const GROUPS: DocGroup[] = [
  {
    title: "Install",
    items: [
      ["need", "What you need"],
      ["start", "Start it"],
      ["google", "Connect Google"],
      ["https", "Use it from other devices"],
    ],
  },
  {
    title: "Run it",
    items: [
      ["admin", "Admin commands"],
      ["update", "Update and back up"],
    ],
  },
];

const ENV = `cat > .env <<EOF
PUBLIC_URL=http://127.0.0.1:3000
POSTGRES_PASSWORD=$(openssl rand -hex 24)
ENCRYPTION_KEYS=k1:$(openssl rand -base64 32)
GOOGLE_CLIENT_ID=
GOOGLE_CLIENT_SECRET=
EOF`;

const COMMANDS = [
  ["status", "Accounts, usage and when each background job last ran"],
  ["users", "List the accounts on this server"],
  ["create-user", "Add someone"],
  ["reset-password", "Set a new password, which they'll have to replace"],
  ["delete-user", "Remove an account. Their files stay in their Drive"],
  ["transfer-admin", "Hand the admin role to someone else"],
  ["sync", "Check every linked account against Drive now"],
  ["links", "List the public Google links on this server"],
  ["upload-limit", "Show or change the largest upload allowed"],
];

export default function SelfHost() {
  const compose = readFileSync(join(process.cwd(), "public", "docker-compose.yml"), "utf8").trimEnd();
  return (
    <DocsShell current="/self-host" title="Self-hosting" lead="Run Tributary on your own machine with Docker. Your files stay in your Google accounts." groups={GROUPS} next={{ href: "/api-guide", label: "API guide" }}>
    <section>
      <h2 id="need">What you need</h2>
      <ul className="plain">
        <li>A machine with Docker and Docker Compose. The images are built for 64-bit Intel and AMD (amd64).</li>
        <li>A Google account to create a free Google Cloud project, which is what lets Tributary reach Drive.</li>
        <li>If you want to use it from other devices, a domain name and HTTPS.</li>
      </ul>
    </section>

    <section>
      <h2 id="start">Start it</h2>
      <p>
        Make a folder, then save this as <code>docker-compose.yml</code> inside it, or{" "}
        <a href="/docker-compose.yml" download>
          download the file
        </a>
        .
      </p>
      <CodeTabs samples={[{ label: "docker-compose.yml", code: compose }]} />
      <p>Create a <code>.env</code> file beside it, with its own random secrets:</p>
      <CodeTabs samples={[{ label: "Shell", code: ENV }]} />
      <p>
        Keep a copy of <code>ENCRYPTION_KEYS</code> somewhere safe. Without it, every linked account has to be set up again.
      </p>
      <p>Start it:</p>
      <CodeTabs samples={[{ label: "Shell", code: "docker compose up -d" }]} />
      <p>
        Open <a href="http://127.0.0.1:3000">http://127.0.0.1:3000</a>. The first account you create becomes the admin, and after that
        only the admin can add people.
      </p>
    </section>

    <section>
      <h2 id="google">Connect Google</h2>
      <p>Tributary uses your own Google project, so nothing passes through anyone else's. In the Google Cloud console:</p>
      <ol className="steps">
        <li>Create a project and enable the <b>Google Drive API</b>.</li>
        <li>On the consent screen, add the <code>drive.file</code> scope.</li>
        <li>Create an OAuth client of type <b>Web application</b>.</li>
        <li>Add these redirect addresses, using your <code>PUBLIC_URL</code>:</li>
      </ol>
      <CodeTabs samples={[{ label: "Redirect addresses", code: "http://127.0.0.1:3000/api/v1/auth/google/callback\nhttp://127.0.0.1:3000/api/v1/storage/google/callback" }]} />
      <p>
        Put the client ID and secret in <code>.env</code>, then run <code>docker compose up -d</code> again. You can now link accounts on
        the Storage page.
      </p>
      <p>
        While the consent screen is in <b>Testing</b>, every account you link has to be listed as a test user, and its sign-in expires
        after 7 days.
      </p>
    </section>

    <section>
      <h2 id="https">Use it from other devices</h2>
      <p>
        Sign-in cookies only work over HTTPS, apart from on <code>127.0.0.1</code>, so a plain address on your network won't keep you signed
        in. Put a reverse proxy such as Caddy or nginx in front of port 3000, then set <code>PUBLIC_URL</code> to the address people use,
        such as <code>https://files.example.com</code>, and update Google's redirect addresses to match.
      </p>
    </section>

    <section>
      <h2 id="admin">Admin commands</h2>
      <p>Run these on the server, from the folder with your compose file:</p>
      <CodeTabs samples={[{ label: "Shell", code: "docker compose exec api /tributary-cli help\ndocker compose exec api /tributary-cli status" }]} />
      <div className="table-wrap">
        <table className="table">
          <tbody>
            {COMMANDS.map(([command, what]) => (
              <tr key={command}>
                <td>
                  <code>{command}</code>
                </td>
                <td>{what}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </section>

    <section>
      <h2 id="update">Update and back up</h2>
      <p>To update, change the version in <code>docker-compose.yml</code>, then run:</p>
      <CodeTabs samples={[{ label: "Shell", code: "docker compose pull\ndocker compose up -d" }]} />
      <p>Database changes apply by themselves when the server starts.</p>
      <p>
        Your files are safe in Drive, but the trash, links, users and keys live in the database, so back that up too:
      </p>
      <CodeTabs samples={[{ label: "Shell", code: "docker compose exec -T postgres pg_dump -U tributary tributary > tributary.sql" }]} />
      <p>
        If the database is ever lost, a sync brings your files and folders back from Drive. The trash, users and keys don't come back.
      </p>
      <p>
        Ready to build on it? Read the <Link href="/api-guide">API guide</Link>, or see the <a href={GITHUB}>code on GitHub</a>.
      </p>
    </section>
    </DocsShell>
  );
}
