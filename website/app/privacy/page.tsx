import type { Metadata } from "next";
import Link from "next/link";

import { DocsShell, type DocGroup } from "@/components/docs-shell";
import { GITHUB } from "@/components/site";

export const metadata: Metadata = {
  title: "Privacy policy",
  description: "What Tributary accesses, how it uses it and where it's stored.",
};

const GROUPS: DocGroup[] = [
  {
    title: "On this page",
    items: [
      ["access", "What Tributary accesses"],
      ["use", "How Tributary uses it"],
      ["stored", "Where it's stored"],
      ["shared", "Who it's shared with"],
      ["google", "Google API Services"],
      ["removing", "Removing your data"],
      ["contact", "Contact"],
    ],
  },
];

export default function Privacy() {
  return (
    <DocsShell current="/privacy" title="Privacy policy" lead="Last updated 7 November 2026." groups={GROUPS} next={{ href: "/terms", label: "Terms of service" }}>
      <p>
        Tributary is self-hosted software. It runs on a computer or server chosen by the person who installs it, and that person
        controls the data. Tributary doesn't send your data to its developer, and there's no central Tributary service.
      </p>

      <section>
        <h2 id="access">What Tributary accesses from Google</h2>
        <ul className="plain">
          <li>
            <strong>If you sign in with Google:</strong> your email address and Google account ID, which identify your account in
            Tributary.
          </li>
          <li>
            <strong>If you link a Google Drive account:</strong> the account's email address and how much storage it has used, plus the
            files Tributary created and any files you choose in Google's picker. Tributary uses the <code>drive.file</code> permission, so it
            can't see anything else in your Drive.
          </li>
        </ul>
      </section>

      <section>
        <h2 id="use">How Tributary uses it</h2>
        <p>
          Tributary uses this information only to sign you in, show you your files, store the files you upload, choose which linked account
          has room, and make the public links you ask for. It isn't used for advertising, and Tributary has no analytics or tracking.
        </p>
      </section>

      <section>
        <h2 id="stored">Where it's stored</h2>
        <ul className="plain">
          <li>Your files stay in your own Google Drive, in a folder named dextro, the app's earlier name.</li>
          <li>Files you add with Google's picker stay where they already are in your Drive.</li>
          <li>
            Your account details, the names and sizes of your files, and the token that keeps a Drive account linked are stored on the
            server running Tributary. Tokens are encrypted with AES-256-GCM.
          </li>
          <li>That server also keeps a security log of actions such as sign-ins, including IP addresses, so its admin can review them.</li>
        </ul>
      </section>

      <section>
        <h2 id="shared">Who it's shared with</h2>
        <p>
          Tributary doesn't sell your data. It sends data only to Google, in order to do what you ask, such as storing a file. The admin of
          the server you use can see your email address and how many accounts you've linked, but not your files through Tributary.
        </p>
        <p>
          If you make a public Google link for a file, anyone with that link can open it. This only happens when you ask for it, one file at
          a time, and only if the admin has turned links on. Google serves the file, so Tributary can't see who opens it. The link ends when
          you remove it, when it expires, or when the file goes to the trash.
        </p>
      </section>

      <section>
        <h2 id="google">Google API Services</h2>
        <p>
          Tributary's use and transfer of information received from Google APIs adheres to the{" "}
          <a href="https://developers.google.com/terms/api-services-user-data-policy">Google API Services User Data Policy</a>, including the
          Limited Use requirements.
        </p>
      </section>

      <section>
        <h2 id="removing">Removing your data</h2>
        <ul className="plain">
          <li>Removing a linked account on the Storage page deletes its token and Tributary's records of its files.</li>
          <li>The files themselves stay in your Google Drive, where you can delete them yourself.</li>
          <li>
            To remove Tributary's access on Google's side, go to{" "}
            <a href="https://myaccount.google.com/connections">your Google account's connections</a>.
          </li>
          <li>The admin of your server can delete your Tributary account. Any public links you made keep working until you remove them in Drive.</li>
        </ul>
      </section>

      <section>
        <h2 id="contact">Contact</h2>
        <p>
          Questions about this policy can go to the <a href={`${GITHUB}/issues`}>issues page of this site's repository</a>. See also the{" "}
          <Link href="/terms">terms of service</Link>.
        </p>
      </section>
    </DocsShell>
  );
}
