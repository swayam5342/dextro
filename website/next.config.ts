import type { NextConfig } from "next";

const config: NextConfig = {
  async rewrites() {
    return {
      beforeFiles: [],
      afterFiles: [],
      // The demo is the real app, built into public/demo. Its own routes all load one page.
      fallback: [
        { source: "/demo", destination: "/demo/index.html" },
        { source: "/demo/:path*", destination: "/demo/index.html" },
      ],
    };
  },
};

export default config;
