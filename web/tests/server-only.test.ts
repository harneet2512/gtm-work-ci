// lib/api/server.ts reads GHOST_API_TOKEN: it must fail the build if a client component ever imports it (PR #66 LOW 11).
import { readFileSync, readdirSync, statSync } from "node:fs";
import path from "node:path";
import { describe, expect, it } from "vitest";

const root = path.resolve(__dirname, "..");
const read = (rel: string) => readFileSync(path.join(root, rel), "utf8");

const files = (dir: string): string[] =>
  readdirSync(path.join(root, dir)).flatMap((f) => {
    const rel = `${dir}/${f}`;
    if (statSync(path.join(root, rel)).isDirectory()) return files(rel);
    return /\.(ts|tsx)$/.test(f) ? [rel] : [];
  });

describe("server-only boundary", () => {
  it.each(["lib/api/server.ts", "lib/cached-loaders.ts"])("%s imports server-only", (rel) => {
    expect(read(rel)).toMatch(/^import "server-only";$/m);
  });

  it("no client component imports the token-reading server module or the cached loaders", () => {
    const clients = [...files("components"), ...files("app")].filter((f) => /^"use client";/m.test(read(f)));
    expect(clients.length).toBeGreaterThan(0);
    for (const f of clients) expect(read(f), f).not.toMatch(/@\/lib\/api\/server|@\/lib\/cached-loaders/);
  });
});
