import Link from "next/link";

export const GITHUB = "https://github.com/swayam5342/tributary";
export const DOCKER_HUB = "https://hub.docker.com/r/swayam5342/tributary-api";

function Mark() {
  return (
    <svg viewBox="0 0 24 24" aria-hidden="true">
      <rect x="2" y="2" width="20" height="20" rx="6" fill="#4cb3f0" />
      <path d="M2 14c3-2.2 6-2.2 10 0s7 2.2 10 0v2a6 6 0 0 1-6 6H8a6 6 0 0 1-6-6z" fill="#0d3a55" />
    </svg>
  );
}

function GitHubIcon() {
  return (
    <svg viewBox="0 0 16 16" aria-hidden="true" fill="currentColor">
      <path d="M8 0C3.58 0 0 3.58 0 8c0 3.54 2.29 6.53 5.47 7.59.4.07.55-.17.55-.38 0-.19-.01-.82-.01-1.49-2.01.37-2.53-.49-2.69-.94-.09-.23-.48-.94-.82-1.13-.28-.15-.68-.52-.01-.53.63-.01 1.08.58 1.23.82.72 1.21 1.87.87 2.33.66.07-.52.28-.87.51-1.07-1.78-.2-3.64-.89-3.64-3.95 0-.87.31-1.59.82-2.15-.08-.2-.36-1.02.08-2.12 0 0 .67-.21 2.2.82a7.6 7.6 0 0 1 4 0c1.53-1.04 2.2-.82 2.2-.82.44 1.1.16 1.92.08 2.12.51.56.82 1.27.82 2.15 0 3.07-1.87 3.75-3.65 3.95.29.25.54.73.54 1.48 0 1.07-.01 1.93-.01 2.2 0 .21.15.46.55.38A8.01 8.01 0 0 0 16 8c0-4.42-3.58-8-8-8" />
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
        <Link href="/self-host">Self-host</Link>
        <Link href="/api-guide">API</Link>
      </div>
      <div className="nav-actions">
        <a className="button soft github" href={GITHUB} aria-label="GitHub">
          <GitHubIcon />
          <span>GitHub</span>
        </a>
        <a className="button" href="/demo/">
          Try the demo
        </a>
      </div>
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
          <a href={`${GITHUB}/blob/main/LICENSE`}>MIT licence</a>
          <Link href="/privacy">Privacy policy</Link>
          <Link href="/terms">Terms of service</Link>
        </div>
      </div>
      <p className="footer-credit">
        Maps: James Rennell and Joseph Tieffenthaler, 1776 to 1812, public domain. Satellite pictures: NASA, public domain.
      </p>
    </footer>
  );
}
