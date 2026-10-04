import type { Metadata } from "next";
import Link from "next/link";
import type { ReactNode } from "react";
import "./globals.css";

export const metadata: Metadata = {
  title: "Ghost",
  description: "Account map, episode replay and decision runs over the Ghost core API.",
};

export default function RootLayout({ children }: { children: ReactNode }) {
  return (
    <html lang="en">
      <body>
        <header className="top">
          <Link href="/" className="brand">
            Ghost
          </Link>
          <nav className="topnav" aria-label="Sections">
            <Link href="/">Accounts</Link>
            <Link href="/replay">Replay</Link>
            <Link href="/runs">Runs</Link>
            <Link href="/knowledge">Knowledge</Link>
          </nav>
          <span className="hint">demo surface over the core</span>
        </header>
        <main>{children}</main>
      </body>
    </html>
  );
}
