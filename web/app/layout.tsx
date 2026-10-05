import { GeistMono } from "geist/font/mono";
import { GeistSans } from "geist/font/sans";
import type { Metadata, Viewport } from "next";
import Link from "next/link";
import { Suspense, type ReactNode } from "react";
import { BrandMark } from "@/components/shell/BrandMark";
import { RailNav } from "@/components/shell/RailNav";
import { InspectorFrame } from "@/components/shell/InspectorFrame";
import { DemoToggle } from "@/components/shell/DemoToggle";
import "./styles/tokens.css";
import "./styles/base.css";
import "./styles/shell.css";
import "./styles/shell-page.css";
import "./styles/components.css";
import "./styles/lists.css";
import "./styles/map.css";
import "./styles/run.css";
import "./styles/evals-marks.css";
import "./styles/evals-head.css";
import "./styles/evals-matrix.css";
import "./styles/evals-cards.css";
import "./styles/evals-actions.css";
import "./styles/evals-catalog.css";
import "./styles/evals-jobs.css";
import "./styles/evals-jobs-knowledge.css";
import "./styles/control.css";
import "./styles/episode.css";
import "./styles/evals-explorer.css";

export const metadata: Metadata = {
  title: "Ghost",
  description: "Ghost reads every account change, drafts the next move, and shows how it judged it.",
};

export const viewport: Viewport = { themeColor: "#ffffff", width: "device-width", initialScale: 1 };

/**
 * The control-plane shell (HAR-145): compact left rail | workspace | collapsible right inspector. The
 * inspector is a parallel-route slot — a page that provides app/<route>/@inspector fills it; everywhere
 * else the frame renders nothing.
 */
export default function RootLayout({
  children,
  inspector,
}: {
  children: ReactNode;
  inspector: ReactNode;
}) {
  return (
    <html lang="en" className={`${GeistSans.variable} ${GeistMono.variable}`}>
      <body>
        <a className="skip-link" href="#main">
          Skip to content
        </a>
        <div className="shell">
          <aside className="rail">
            <Link href="/" className="brand" aria-label="Ghost home">
              <BrandMark />
              <span translate="no">Ghost</span>
            </Link>
            <Suspense fallback={null}>
              <RailNav />
            </Suspense>
            <span className="env" title="Replayed CRM data over the Ghost core">
              Demo workspace
            </span>
          </aside>
          <div className="shell-col">
            <header className="top">
              <Suspense>
                <DemoToggle />
              </Suspense>
            </header>
            <main id="main" tabIndex={-1}>
              {children}
            </main>
          </div>
          <InspectorFrame>{inspector}</InspectorFrame>
        </div>
      </body>
    </html>
  );
}
