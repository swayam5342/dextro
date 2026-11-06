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
      <svg className="river tall" viewBox="0 0 300 460" role="img" aria-label="Three streams of 15 gigabytes each join into one river that opens into a pool of 45 gigabytes.">
        <path d="M36 0 C 30 90 66 130 92 180 S 132 240 138 290" fill="none" stroke="#4cb3f0" strokeWidth="14" />
        <path d="M150 0 C 136 80 168 130 150 190 S 146 240 150 290" fill="none" stroke="#4cb3f0" strokeWidth="14" />
        <path d="M264 0 C 272 90 232 130 212 180 S 170 240 162 290" fill="none" stroke="#4cb3f0" strokeWidth="14" />
        <path d="M130 270 C 128 310 112 330 92 350 C 80 362 40 372 0 380 L 0 460 L 300 460 L 300 380 C 260 372 220 362 208 350 C 188 330 172 310 170 270 Z" fill="#4cb3f0" />
        <text x="150" y="432" textAnchor="middle" fill="#00263a" fontSize="46" fontWeight="700" letterSpacing="-1">45 GB</text>
      </svg>
    </>
  );
}
