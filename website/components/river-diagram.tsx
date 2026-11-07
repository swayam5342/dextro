const label = { fill: "#fff", fontSize: 22, fontWeight: 600 };

/** Three streams joining into one river that opens into the pool, all one colour. */
export function RiverDiagram() {
  return (
    <>
      <svg className="river wide" viewBox="0 0 1200 300" role="img" aria-label="Three streams of 15 gigabytes each join into one river that opens into a pool of 45 gigabytes.">
        <path d="M0 36 C 150 30 210 66 300 92 S 450 132 560 138" fill="none" stroke="#4cb3f0" strokeWidth="16" />
        <path d="M0 150 C 120 136 220 168 330 150 S 470 146 560 150" fill="none" stroke="#4cb3f0" strokeWidth="16" />
        <path d="M0 264 C 140 272 220 232 310 212 S 450 170 560 162" fill="none" stroke="#4cb3f0" strokeWidth="16" />
        <path d="M530 130 C 640 128 700 112 780 92 C 830 80 860 40 900 0 L 1200 0 L 1200 300 L 900 300 C 860 260 830 220 780 208 C 700 188 640 172 530 170 Z" fill="#4cb3f0" />
        <text x="0" y="18" {...label}>15 GB</text>
        <text x="0" y="132" {...label}>15 GB</text>
        <text x="0" y="246" {...label}>15 GB</text>
        <text x="1050" y="172" textAnchor="middle" fill="#00263a" fontSize="64" fontWeight="700" letterSpacing="-2">45 GB</text>
      </svg>
      {/* The same river for a small screen, shorter, so its labels stay readable. */}
      <svg className="river compact" viewBox="0 0 720 300" role="img" aria-label="Three streams of 15 gigabytes each join into one river that opens into a pool of 45 gigabytes.">
        <path d="M0 60 C 90 54 130 84 190 106 S 270 136 340 140" fill="none" stroke="#4cb3f0" strokeWidth="15" />
        <path d="M0 158 C 80 146 140 170 200 156 S 280 150 340 150" fill="none" stroke="#4cb3f0" strokeWidth="15" />
        <path d="M0 274 C 90 280 130 238 190 214 S 270 168 340 160" fill="none" stroke="#4cb3f0" strokeWidth="15" />
        <path d="M320 128 C 380 126 420 112 470 92 C 505 78 525 40 550 0 L 720 0 L 720 300 L 550 300 C 525 260 505 222 470 208 C 420 188 380 174 320 172 Z" fill="#4cb3f0" />
        <text x="0" y="34" fill="#fff" fontSize="32" fontWeight="600">15 GB</text>
        <text x="0" y="134" fill="#fff" fontSize="32" fontWeight="600">15 GB</text>
        <text x="0" y="250" fill="#fff" fontSize="32" fontWeight="600">15 GB</text>
        <text x="630" y="172" textAnchor="middle" fill="#00263a" fontSize="60" fontWeight="700" letterSpacing="-2">45 GB</text>
      </svg>
    </>
  );
}
