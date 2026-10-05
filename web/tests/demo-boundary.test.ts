// HAR-129 demo boundary (contracts/demo/boundary.v1.json): the web control plane is one of the two
// audience surfaces, so no user-visible string in web/app, web/components, web/lib/view or the eval
// wording names developer tooling, terminal commands, proof plumbing or out-of-scope experiments, and
// the visible trigger is the web Play control calling the real core endpoint. Comments and import paths
// are internal and are not scanned.
import { readdirSync, readFileSync } from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import ts from "typescript";
import { describe, expect, it, vi } from "vitest";
import { createCoreClient } from "@/lib/api/core-client";
import { CONTRACTS_DIR, readJson } from "./contract-validator";

interface Term {
  id: string;
  kind: string;
  pattern: string;
  case_sensitive: boolean;
}
interface Boundary {
  invariant: string;
  visible_trigger: { surface: string; control: string; core_endpoint: string };
  forbidden_terms: Term[];
}
interface Hit {
  term: string;
  match: string;
}

const WEB = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const boundary = readJson<Boundary>(path.join(CONTRACTS_DIR, "demo", "boundary.v1.json"));
const terms = boundary.forbidden_terms.map((t) => ({ id: t.id, re: new RegExp(t.pattern, t.case_sensitive ? "g" : "gi") }));

function scan(text: string): Hit[] {
  return terms.flatMap(({ id, re }) => [...text.matchAll(re)].map((m) => ({ term: id, match: m[0] })));
}

/** Every string a component or view model can put on screen: literals, template text and JSX text. */
function visibleStrings(source: string, fileName: string): { line: number; text: string }[] {
  const sf = ts.createSourceFile(fileName, source, ts.ScriptTarget.Latest, true, fileName.endsWith(".tsx") ? ts.ScriptKind.TSX : ts.ScriptKind.TS);
  const out: { line: number; text: string }[] = [];
  const add = (node: ts.Node, text: string) => {
    if (text.trim()) out.push({ line: sf.getLineAndCharacterOfPosition(node.getStart(sf)).line + 1, text });
  };
  const visit = (node: ts.Node): void => {
    if (ts.isImportDeclaration(node) || ts.isExportDeclaration(node) || ts.isImportTypeNode(node)) return;
    if (ts.isStringLiteral(node) || ts.isNoSubstitutionTemplateLiteral(node)) add(node, node.text);
    else if (ts.isTemplateHead(node) || ts.isTemplateMiddle(node) || ts.isTemplateTail(node)) add(node, node.text);
    else if (ts.isJsxText(node)) add(node, node.text);
    ts.forEachChild(node, visit);
  };
  visit(sf);
  return out;
}

function sourceFiles(dir: string): string[] {
  return readdirSync(dir, { withFileTypes: true }).flatMap((e) => {
    const p = path.join(dir, e.name);
    if (e.isDirectory()) return sourceFiles(p);
    return /\.(ts|tsx)$/.test(e.name) && !e.name.endsWith(".d.ts") ? [p] : [];
  });
}

function jsonStrings(v: unknown): string[] {
  if (typeof v === "string") return [v];
  if (Array.isArray(v)) return v.flatMap(jsonStrings);
  if (v && typeof v === "object") return Object.values(v).flatMap(jsonStrings);
  return [];
}

const AUDIENCE_SOURCES = [
  ...sourceFiles(path.join(WEB, "app")),
  ...sourceFiles(path.join(WEB, "components")),
  ...sourceFiles(path.join(WEB, "lib")).filter((f) => !f.includes(`${path.sep}api${path.sep}`)),
];

describe("the forbidden-term scan", () => {
  it("catches a seeded CLI line in rendered copy", () => {
    const tsx = 'export const P = () => <p className="hint">Enter the id printed by <code>ghostctl freeze</code>.</p>;';
    const hits = visibleStrings(tsx, "seeded.tsx").flatMap((s) => scan(s.text));
    expect(hits.map((h) => h.term)).toContain("ghostctl");
  });

  it("catches seeded terminal, proof and experiment copy in strings and templates", () => {
    const src = [
      'const a = "Open a terminal and run demo.ps1 play";',
      "const b = `See the confirmation matrix for ${run}`;",
      'const c = { label: "Blind A/B/C knowledge uplift" };',
    ].join("\n");
    const hits = visibleStrings(src, "seeded.ts").flatMap((s) => scan(s.text)).map((h) => h.term);
    expect(hits).toEqual(expect.arrayContaining(["terminal_phrases", "shell_scripts", "proof_plumbing", "abc_experiment", "uplift"]));
  });

  it("ignores comments and import paths, which are internal", () => {
    const src = [
      'import { x } from "../scripts/demo/x";',
      "// ghostctl freeze prints the manifest id",
      "/* the counterfactual draft is never rendered; go test covers it */",
      'export const label = "Play next";',
    ].join("\n");
    expect(visibleStrings(src, "clean.ts").flatMap((s) => scan(s.text))).toEqual([]);
  });

  it("leaves product language alone", () => {
    for (const text of ["Play next", "Strategy B (recommended)", "CONFIRMED transitions are terminal", "Rule check", "Inspect evals", "You chose B over A", "Choose Strategy A/B/C", "a 5% price uplift"]) {
      expect(scan(text)).toEqual([]);
    }
  });
});

describe("web control plane copy stays inside the demo boundary", () => {
  it("scans a real set of audience sources", () => {
    expect(AUDIENCE_SOURCES.length).toBeGreaterThan(20);
  });

  it("names no developer tooling, terminal command, proof plumbing or out-of-scope experiment", () => {
    const violations = AUDIENCE_SOURCES.flatMap((file) =>
      visibleStrings(readFileSync(file, "utf8"), file).flatMap(({ line, text }) =>
        scan(text).map((h) => `${path.relative(WEB, file)}:${line}: ${h.term} "${h.match}"`),
      ),
    );
    expect(violations).toEqual([]);
  });

  it("keeps the eval wording table (Slack and web) inside the boundary", () => {
    const wording = readJson(path.join(CONTRACTS_DIR, "evals", "eval_wording.json"));
    expect(jsonStrings(wording).flatMap(scan)).toEqual([]);
  });
});

// The /evals page renders eval-registry family names and invariants, eval names and completeness-matrix
// surface names (lib/evals/registry.ts). These still carry out-of-scope experiment wording from the HAR-97
// taxonomy; the rewrite belongs to the eval control-plane work (PR #66). The ratchet fails when a new
// violation appears and when a listed one is fixed, so the list only shrinks.
const KNOWN_REGISTRY_DRIFT = [
  "families[E16].invariant: abc_experiment",
  "matrix.surfaces[18].name: negative_transfer",
];

describe("eval registry text the web renders", () => {
  it("only carries the listed, owned drift (ratchet)", () => {
    const registry = readJson<{ families: { id: string; name: string; invariant: string | null }[]; evals: { id: string; name: string }[] }>(
      path.join(CONTRACTS_DIR, "evals", "eval_registry.json"),
    );
    const matrix = readJson<{ surfaces: { id: number; name: string }[] }>(path.join(CONTRACTS_DIR, "evals", "completeness_matrix.json"));
    const found = new Set<string>();
    const check = (where: string, text: string | null) => scan(text ?? "").forEach((h) => found.add(`${where}: ${h.term}`));
    for (const f of registry.families) {
      check(`families[${f.id}].name`, f.name);
      check(`families[${f.id}].invariant`, f.invariant);
    }
    for (const e of registry.evals) check(`evals[${e.id}].name`, e.name);
    for (const s of matrix.surfaces) check(`matrix.surfaces[${s.id}].name`, s.name);
    expect([...found].sort()).toEqual([...KNOWN_REGISTRY_DRIFT].sort());
  });
});

describe("the visible trigger is web Play on the real core", () => {
  it("is Play in the web control plane", () => {
    expect(boundary.visible_trigger.surface).toBe("web_control_plane");
    expect(boundary.visible_trigger.control).toBe("Play");
  });

  it("posts exactly the core endpoint the boundary names", async () => {
    const calls: { url: URL; init: RequestInit }[] = [];
    const fetchImpl = vi.fn(async (input: string | URL | Request, init?: RequestInit) => {
      calls.push({ url: new URL(String(input)), init: init ?? {} });
      return new Response(JSON.stringify({ episode: 1, total: 2, released: {} }), { status: 200, headers: { "content-type": "application/json" } });
    }) as unknown as typeof fetch;
    const id = "0d3a0000-0000-4000-8000-000000000501";
    await createCoreClient({ baseUrl: "http://core.test:8080", token: "t", fetchImpl }).advanceReplayEpisode(id);
    const [method, route] = boundary.visible_trigger.core_endpoint.split(" ");
    expect(calls[0]!.init.method).toBe(method);
    expect(calls[0]!.url.pathname).toBe(route!.replace("{manifest_id}", id));
  });

  it("is wired from a server action to a Play button, not a script", () => {
    const appFiles = sourceFiles(path.join(WEB, "app"));
    const serverActions = appFiles.filter((f) => /^["']use server["']/m.test(readFileSync(f, "utf8")));
    expect(serverActions.some((f) => readFileSync(f, "utf8").includes("core().advanceReplayEpisode("))).toBe(true);
    const buttons = sourceFiles(path.join(WEB, "components")).flatMap((f) =>
      visibleStrings(readFileSync(f, "utf8"), f).filter(({ text }) => /^\s*Play\b/.test(text)),
    );
    expect(buttons.length).toBeGreaterThan(0);
  });
});
