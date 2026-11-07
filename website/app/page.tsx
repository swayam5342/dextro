import Link from "next/link";
import type { CSSProperties } from "react";

import { FeaturesMenu } from "@/components/features-menu";
import { MapPlates } from "@/components/map-plates";
import { RiverDiagram } from "@/components/river-diagram";
import { RoutingLab } from "@/components/routing-lab";
import { SiteFooter, SiteNav } from "@/components/site";

const step = (i: number) => ({ "--i": i }) as CSSProperties;

const LEDGER = [
  ["Your files", "Your Google Drive", "You", "They stay in Drive"],
  ["Encrypted files", "Your Google Drive, encrypted", "Your server", "They stay in Drive, unreadable without your key"],
  ["Folders", "Drive and your database", "You", "They stay in Drive"],
  ["The trash", "Your database", "You", "The list goes, the files stay"],
  ["Google tokens", "Your database, encrypted", "Your server", "Revoke them at Google"],
  ["API keys", "Your database, hashed", "Whoever holds the key", "They stop working"],
  ["Public links", "Google", "Anyone with the link", "They keep working"],
];

export default function Home() {
  return (
    <>
      <SiteNav />
      <main>
        <section className="hero">
          <h1>
            <span>Many Drives,</span>
            <span>one pool</span>
          </h1>
          <p className="hero-text">Tributary is an open-source, self-hosted file manager that joins your Google Drive accounts into one pool of storage.</p>
          <div className="hero-actions">
            <a className="button" href="/demo/">
              Try the demo
            </a>
            <Link className="button soft" href="/self-host">
              Host your own
            </Link>
          </div>
          <figure className="rise">
            <video autoPlay muted loop playsInline poster="/media/files-dark.webp" width={1280} height={720} aria-label="A tour of Tributary: viewing a photo, uploading files, moving a file into a folder and making a link">
              <source src="/media/demo.mp4" type="video/mp4" />
            </video>
          </figure>
        </section>

        <section id="how-it-works" className="section" aria-labelledby="join-title">
          <div className="join-head">
            <h2 id="join-title" data-rise>
              Many into one
            </h2>
            <p data-rise style={step(1)}>
              A tributary is a small river that joins a larger one, the way the Yamuna and the Kosi join the Ganga. Each Google
              account gives you 15 GB free, so three accounts hold 45 GB. Tributary shows them as one place to put things.
            </p>
          </div>
          <div data-rise style={step(2)}>
            <RiverDiagram />
          </div>
          <div className="zones">
            <div data-rise style={step(1)}>
              <h3>Link your accounts</h3>
              <p>Sign in to each Drive once. Tributary asks Google only for files it creates or that you pick, never your whole Drive.</p>
            </div>
            <div data-rise style={step(2)}>
              <h3>See one pool</h3>
              <p>Free space adds up across accounts, and everything sits in one set of folders, whichever account holds it.</p>
            </div>
            <div data-rise style={step(3)}>
              <h3>Keep your files</h3>
              <p>Each file sits whole in one of your own accounts, so the largest upload is what one account can hold. Stop using Tributary and they're all still in Drive, though anything you encrypted needs your server's key to read.</p>
            </div>
          </div>
        </section>

        <MapPlates />

        <RoutingLab />

        <FeaturesMenu />

        <section className="ledger-band" aria-labelledby="ledger-title">
          <h2 id="ledger-title" data-rise>
            Where everything lives
          </h2>
          <table className="ledger" data-rise style={step(1)}>
            <thead>
              <tr>
                <th scope="col">What</th>
                <th scope="col">Kept in</th>
                <th scope="col">Who can reach it</th>
                <th scope="col">If you stop using Tributary</th>
              </tr>
            </thead>
            <tbody>
              {LEDGER.map(([what, kept, who, after]) => (
                <tr key={what}>
                  <td>{what}</td>
                  <td data-label="Kept in">{kept}</td>
                  <td data-label="Reached by">{who}</td>
                  <td data-label="If you stop">{after}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </section>

        <section className="section" aria-labelledby="about-title">
          <h2 id="about-title" data-rise>
            About
          </h2>
          <p className="about-text" data-rise style={step(1)}>
            Tributary used to be called <mark>dextro</mark>, which is why the folder in your Drive still has that name. It's a{" "}
            <mark>Go</mark> server, a small Node web server and <mark>PostgreSQL</mark>, and it runs on your own machine with Docker.
            One admin runs each server and creates accounts for other people, and each person links their own Drives and sees only
            their own files. There are <mark>no analytics</mark>, and the only outside service it talks to is Google, on your behalf.
            It uses a narrow Drive permission, so it sees what it created and what you pick, and nothing else. The code is on GitHub
            under the <mark>MIT licence</mark>, so you can read it, run it and change it.{" "}
            <mark className="bright">Your storage, joined up, on your own terms.</mark>
          </p>
        </section>

        <Link className="band" href="/self-host">
          <h2>Install</h2>
          <span className="band-side">
            <span>Two Docker images, PostgreSQL and a free Google project of your own.</span>
            <svg viewBox="0 0 48 48" aria-hidden="true">
              <path d="M10 38 38 10M14 10h24v24" fill="none" stroke="currentColor" strokeWidth="5" />
            </svg>
          </span>
        </Link>
      </main>
      <SiteFooter />
    </>
  );
}
