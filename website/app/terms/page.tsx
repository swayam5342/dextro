import type { Metadata } from "next";
import Link from "next/link";

import { DocsShell, type DocGroup } from "@/components/docs-shell";
import { GITHUB } from "@/components/site";

export const metadata: Metadata = {
  title: "Terms of service",
  description: "The terms for using Tributary, a self-hosted web app that stores your files in your own Google Drive accounts.",
};

const GROUPS: DocGroup[] = [
  {
    title: "On this page",
    items: [
      ["what", "What Tributary is"],
      ["responsibilities", "Your responsibilities"],
      ["files", "Your files"],
      ["server", "If you use someone else's server"],
      ["warranty", "No warranty"],
      ["liability", "Limit of liability"],
      ["changes", "Changes"],
      ["contact", "Contact"],
    ],
  },
];

export default function Terms() {
  return (
    <DocsShell current="/terms" title="Terms of service" lead="Last updated 6 November 2026." groups={GROUPS} next={{ href: "/privacy", label: "Privacy policy" }}>
      <p>
        These terms cover your use of Tributary, a self-hosted web app that stores your files in your own Google Drive accounts. By using
        Tributary, you agree to them. If you don't agree, don't use it.
      </p>

      <section>
        <h2 id="what">What Tributary is</h2>
        <p>
          Tributary is software, not a hosted service. It runs on a computer or server chosen by the person who installs it, and there's
          no central Tributary service. Your files stay in your own Google Drive accounts. Tributary isn't made by, affiliated with, or
          endorsed by Google.
        </p>
      </section>

      <section>
        <h2 id="responsibilities">Your responsibilities</h2>
        <ul className="plain">
          <li>Keep your Tributary password, API keys and the server running Tributary secure.</li>
          <li>Keep your own backups of anything you can't afford to lose.</li>
          <li>Only link Google accounts you're allowed to use, and follow Google's own terms when you do.</li>
          <li>Don't use Tributary to store or share anything illegal, or anything you don't have the right to use.</li>
        </ul>
      </section>

      <section>
        <h2 id="files">Your files</h2>
        <p>
          Your files remain yours. Tributary claims no rights over them, and only handles them to do what you ask, such as storing or
          showing a file. How Tributary handles your data is covered in the <Link href="/privacy">privacy policy</Link>.
        </p>
      </section>

      <section>
        <h2 id="server">If you use someone else's server</h2>
        <p>
          The admin of a Tributary server decides who has an account there, and can reset passwords and delete accounts. Deleting a
          Tributary account doesn't delete the files in your Google Drive.
        </p>
      </section>

      <section>
        <h2 id="warranty">No warranty</h2>
        <p>
          Tributary is provided as is, without warranties of any kind, whether express or implied. It may contain bugs, may stop working,
          and can change at any time. Your use of it depends on Google's services, which are outside Tributary's control.
        </p>
      </section>

      <section>
        <h2 id="liability">Limit of liability</h2>
        <p>
          To the extent the law allows, Tributary's developer isn't liable for any loss or damage from using Tributary or from not being
          able to use it, including lost files or data.
        </p>
      </section>

      <section>
        <h2 id="changes">Changes</h2>
        <p>These terms can change. When they do, this page and its date are updated.</p>
      </section>

      <section>
        <h2 id="contact">Contact</h2>
        <p>
          Questions about these terms can go to the <a href={`${GITHUB}/issues`}>issues page of this site's repository</a>.
        </p>
      </section>
    </DocsShell>
  );
}
