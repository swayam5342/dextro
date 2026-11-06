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
      <svg className="river tall" viewBox="0 0 360 290" role="img" aria-label="Three streams of 15 gigabytes each join into one river that opens into a pool of 45 gigabytes.">
        <path d="M50 30 C 50 100 130 120 172 190" fill="none" stroke="#4cb3f0" strokeWidth="10" />
        <path d="M180 30 C 165 90 195 130 180 190" fill="none" stroke="#4cb3f0" strokeWidth="10" />
        <path d="M310 30 C 310 100 230 120 188 190" fill="none" stroke="#4cb3f0" strokeWidth="10" />
        <path d="M160 175 C 150 212 100 222 0 232 L 0 290 L 360 290 L 360 232 C 260 222 210 212 200 175 Z" fill="#4cb3f0" />
        {[50, 180, 310].map((x) => (
          <text key={x} x={x} y="16" textAnchor="middle" fill="#fff" fontSize="15" fontWeight="600">
            15 GB
          </text>
        ))}
        <text x="180" y="274" textAnchor="middle" fill="#00263a" fontSize="34" fontWeight="700" letterSpacing="-1">
          45 GB
        </text>
      </svg>
    </>
  );
}
