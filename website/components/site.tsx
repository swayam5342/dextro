import Link from "next/link";

export const GITHUB = "https://github.com/swayam5342/tributary";
export const DOCKER_HUB = "https://hub.docker.com/r/swayam5342/tributary-api";
export const PRIVACY = "https://swayamcyber.in/dextro-site/privacy.html";

function Mark() {
  return (
    <svg viewBox="0 0 24 24" aria-hidden="true">
      <rect x="2" y="2" width="20" height="20" rx="6" fill="#4cb3f0" />
      <path d="M2 14c3-2.2 6-2.2 10 0s7 2.2 10 0v2a6 6 0 0 1-6 6H8a6 6 0 0 1-6-6z" fill="#0d3a55" />
    </svg>
  );
}

export function SiteNav() {
  return (
    <nav className="nav" aria-label="Main">
      <Link className="brand" href="/">
        <Mark />
        Tributary
      </Link>
      <div className="nav-links">
        <Link href="/#how-it-works">How it works</Link>
        <Link href="/api-guide">API</Link>
        <Link href="/self-host">Self-host</Link>
        <a href={GITHUB}>GitHub</a>
      </div>
      <a className="button" href="/demo/">
        Try the demo
      </a>
    </nav>
  );
}

export function SiteFooter() {
  return (
    <footer className="footer">
      <div className="footer-top">
        <div className="footer-call">
          <h2>
            Your Drives.
            <br />
            Your server.
          </h2>
          <a className="button" href={GITHUB}>
            Get the code on GitHub
          </a>
        </div>
        <div className="footer-links">
          <a href="/demo/">Demo</a>
          <Link href="/self-host">Self-host</Link>
          <Link href="/api-guide">API guide</Link>
          <a href={GITHUB}>GitHub</a>
          <a href={DOCKER_HUB}>Docker Hub</a>
          <a href={PRIVACY}>Privacy</a>
          <a href={`${GITHUB}/blob/main/LICENSE`}>MIT licence</a>
        </div>
      </div>
      <p className="footer-credit">
        Maps: Harold N. Fisk, US Army Corps of Engineers, 1944, public domain. Satellite pictures in the demo: NASA, public domain.
      </p>
      <p className="footer-name" aria-hidden="true">
        Tributary
      </p>
    </footer>
  );
}
