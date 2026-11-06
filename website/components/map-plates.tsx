"use client";

import { useEffect, useState } from "react";

const PLATES = [
  { src: "/media/ganga-rennell-1786.webp", alt: "James Rennell's 1786 map of the Ganga plain, tinted pink, running down to the Bay of Bengal" },
  { src: "/media/ganga-actual-survey.webp", alt: "Rennell's hand-coloured survey of Bengal and Bihar, with the Ganga and its tributaries drawn across each province" },
  { src: "/media/ganga-delta.webp", alt: "The Ganga delta from space, its channels spreading through dark mangrove forest to the sea" },
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
        {PLATES.map(({ src, alt }, index) => (
          // eslint-disable-next-line @next/next/no-img-element
          <img key={src} src={src} alt={index === shown ? alt : ""} width={1600} height={800} loading="lazy" className={index === shown ? "on" : undefined} />
        ))}
      </div>
      <figcaption className="plates-caption">
        <span>James Rennell's surveys of the Ganga from 1776 and 1786, and its delta seen from space.</span>
        <span>Rennell and NASA, public domain</span>
      </figcaption>
    </figure>
  );
}
