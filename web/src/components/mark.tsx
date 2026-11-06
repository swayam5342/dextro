/** dextro's mark: water in a square, for a pool of storage. */
export function Mark({ className }: { className?: string }) {
  return (
    <svg viewBox="0 0 24 24" className={className} aria-hidden="true">
      <rect x="2" y="2" width="20" height="20" rx="6" fill="var(--water)" />
      <path d="M2 14c3-2.2 6-2.2 10 0s7 2.2 10 0v2a6 6 0 0 1-6 6H8a6 6 0 0 1-6-6z" fill="var(--water-ink)" />
    </svg>
  );
}
