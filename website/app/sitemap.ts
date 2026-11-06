import type { MetadataRoute } from "next";

const BASE = "https://tributary.swayam.li";

export default function sitemap(): MetadataRoute.Sitemap {
  return ["", "/self-host", "/api-guide", "/privacy", "/terms"].map((path) => ({ url: `${BASE}${path}` }));
}
