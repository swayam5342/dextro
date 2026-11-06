import "@fontsource/instrument-sans/400.css";
import "@fontsource/instrument-sans/500.css";
import "@fontsource/instrument-sans/600.css";
import "@fontsource/instrument-sans/700.css";
import "./globals.css";

import type { Metadata } from "next";
import type { ReactNode } from "react";

import { Reveal } from "@/components/reveal";

export const metadata: Metadata = {
  title: { default: "Tributary: many Google Drives, one pool", template: "%s | Tributary" },
  description: "An open-source, self-hosted file manager that joins your Google Drive accounts into one pool of storage.",
  icons: { icon: "/mark.svg" },
  metadataBase: new URL("https://tributary.swayam.li"),
  openGraph: {
    title: "Tributary: many Google Drives, one pool",
    description: "An open-source, self-hosted file manager that joins your Google Drive accounts into one pool of storage.",
    url: "/",
    siteName: "Tributary",
    type: "website",
    images: [{ url: "/og.jpg", width: 1200, height: 630, alt: "Tributary: many Drives, one pool" }],
  },
  twitter: { card: "summary_large_image" },
};

export default function RootLayout({ children }: { children: ReactNode }) {
  return (
    <html lang="en">
      <body>
        <Reveal />
        {children}
      </body>
    </html>
  );
}
