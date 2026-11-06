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
