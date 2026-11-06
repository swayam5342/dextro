"use client";

import { useEffect, useState } from "react";

const PLATES: { src: string; alt: string; legendRight?: boolean }[] = [
  { src: "/media/plate-07-wide.webp", alt: "A 1944 map of the Mississippi, its old courses looping across the valley in pink, green and blue" },
  { src: "/media/plate-05-wide.webp", alt: "Another sheet of the same map, where the river's past channels cross and braid", legendRight: true },
  { src: "/media/plate-10-wide.webp", alt: "A third sheet, with old river bends drawn in layers of colour" },
];

/** The survey maps change slowly, like light in a room. */
export function MapPlates() {
  const [shown, setShown] = useState(0);
  useEffect(() => {
    if (matchMedia("(prefers-reduced-motion: reduce)").matches) {
      return;
    }
    const timer = setInterval(() => {
      if (!document.hidden) {
        setShown((current) => (current + 1) % PLATES.length);
      }
    }, 5500);
    return () => clearInterval(timer);
  }, []);
  return (
    <figure className="plates-wrap">
      <div className="plates">
        {PLATES.map(({ src, alt, legendRight }, index) => (
          // eslint-disable-next-line @next/next/no-img-element
          <img key={src} src={src} alt={index === shown ? alt : ""} width={1920} height={1080} loading="lazy" className={index === shown ? "on" : undefined} style={legendRight ? { transformOrigin: "0 50%" } : undefined} />
        ))}
      </div>
      <figcaption className="plates-caption">
        <span>Harold Fisk's 1944 survey of the Mississippi. Every colour is a course the river once ran.</span>
        <span>US Army Corps of Engineers, public domain</span>
      </figcaption>
    </figure>
  );
}
