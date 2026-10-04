import type { Metadata } from "next";
import Link from "next/link";
import type { ReactNode } from "react";
import "./globals.css";

export const metadata: Metadata = {
  title: "Ghost: account map",
  description: "Read-only account map, timeline and provenance over the Ghost core API.",
};

export default function RootLayout({ children }: { children: ReactNode }) {
  return (
    <html lang="en">
      <body>
        <header className="top">
          <Link href="/" className="brand">
            Ghost
          </Link>
          <span className="hint">read-only view of the core</span>
        </header>
        <main>{children}</main>
      </body>
    </html>
  );
}
