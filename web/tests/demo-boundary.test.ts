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
import { buildOverview, loadEvalContracts } from "@/lib/evals/registry";
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

// The /evals page renders eval-registry family names and invariants, eval names and completeness-matrix surface names.
// The registry contract still carries out-of-scope experiment wording (the E16 "B over A / C equals A" invariant, the
// "negative transfer" surface, E16.5, E22.4); the WEB CATALOG (lib/evals/registry.ts) hides or rewords it, so what the
// page renders is clean. The ratchet scans the rendered catalog, so it is empty, and it fails if drift ever returns.
const KNOWN_REGISTRY_DRIFT: string[] = [];

describe("eval registry text the web renders", () => {
  const overview = buildOverview(loadEvalContracts(CONTRACTS_DIR), null);

  it("carries no out-of-scope experiment wording in the web catalog (ratchet)", () => {
    const found = new Set<string>();
    const check = (where: string, text: string | null) => scan(text ?? "").forEach((h) => found.add(`${where}: ${h.term}`));
    for (const f of overview.families) {
      check(`families[${f.id}].name`, f.name);
      check(`families[${f.id}].invariant`, f.invariant);
      for (const e of f.evals) {
        check(`evals[${e.id}].name`, e.name);
        e.surfaces.forEach((s) => check(`evals[${e.id}].surface`, s));
      }
    }
    for (const g of overview.groups) for (const r of g.rows) [r.name, r.question, r.definition, r.blockingRule, ...r.surfaces].forEach((t) => check(`checks[${r.evalType}]`, t));
    expect([...found].sort()).toEqual([...KNOWN_REGISTRY_DRIFT].sort());
  });

  it("leaves E16.5 and E22.4 out and does not claim B over A or C equals A", () => {
    const rendered = jsonStrings(overview).join("\n");
    expect(overview.families.flatMap((f) => f.evals.map((e) => e.id))).not.toEqual(expect.arrayContaining(["E16.5"]));
    expect(overview.families.flatMap((f) => f.evals.map((e) => e.id))).not.toEqual(expect.arrayContaining(["E22.4"]));
    expect(rendered).not.toMatch(/B over A|C equals A|negative[- ]transfer/i);
  });

  it("is still true of the registry contract that the drift exists there (so the web filter is load-bearing)", () => {
    const registry = readJson<{ families: { id: string; invariant: string | null }[] }>(path.join(CONTRACTS_DIR, "evals", "eval_registry.json"));
    expect(scan(registry.families.find((f) => f.id === "E16")!.invariant ?? "").map((h) => h.term)).toContain("abc_experiment");
  });
});

// Internal references never reach an audience-facing string: ticket and work-package ids, endpoint paths, environment
// variable names, decision-record and benchmark file pointers, and "the core does not serve it" phrasing. The eval wording
// table (lib/evals/vocabulary.ts) mirrors a contract whose `description` metadata is never rendered, so it is exempt.
const INTERNAL_REFERENCES: { id: string; re: RegExp }[] = [
  { id: "ticket_id", re: /\b(?:HAR|WP)-?\d+\b/ },
  { id: "decision_record", re: /\bADR-\d+\b|§\s?\d/ },
  { id: "endpoint_path", re: /\b(?:GET|POST|PUT|PATCH|DELETE) \/\S|\/(?:healthz|provider-breaker|outbox|eval-runs|knowledge-mutations|replay\/manifests)\b/ },
  { id: "env_var", re: /\b(?:GHOST_[A-Z][A-Z_]{2,}|CORE_URL)\b/ },
  { id: "repo_path", re: /\b(?:bench|contracts|scripts|core-go)\/[\w.-]+/ },
  { id: "core_internals", re: /\bnot served(?: by the core)?\b|\bthe core (?:does not|did not|doesn't) (?:serve|answer)\b|\bno endpoint (?:serves|returns)\b/i },
  { id: "pr_reference", re: /\bPR #\d+\b/ },
  { id: "setup_wording", re: /\bfrozen\b/i },
];

describe("audience copy names no internal reference", () => {
  it("has no ticket id, endpoint path, environment variable, repo path or 'not served by the core' phrasing", () => {
    const exempt = path.join("lib", "evals", "vocabulary.ts");
    const violations = AUDIENCE_SOURCES.filter((f) => !f.endsWith(exempt)).flatMap((file) =>
      visibleStrings(readFileSync(file, "utf8"), file).flatMap(({ line, text }) =>
        INTERNAL_REFERENCES.filter(({ re }) => re.test(text)).map(({ id }) => `${path.relative(WEB, file)}:${line}: ${id} "${text.trim().slice(0, 60)}"`),
      ),
    );
    expect(violations).toEqual([]);
  });

  it("catches seeded internal references", () => {
    const src = [
      'const a = "Counted by HAR-97 E7";',
      'const b = "Agent runs, newest first (GET /runs).";',
      "const c = <p>The core did not answer <code>/provider-breaker</code>.</p>;",
      'const d = "Set GHOST_API_TOKEN before starting";',
      'const e = "Per-change verdicts are not served by the core yet.";',
    ].join("\n");
    const hit = (t: string) => INTERNAL_REFERENCES.filter(({ re }) => re.test(t)).map((r) => r.id);
    const found = visibleStrings(src, "seeded.tsx").flatMap(({ text }) => hit(text));
    expect(found).toEqual(expect.arrayContaining(["ticket_id", "endpoint_path", "core_internals", "env_var"]));
    expect(hit("The next action")).toEqual([]);
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

  it("is the only trigger: no button of the audience app continues into, or switches to, another account or resets the demo", () => {
    const labels = sourceFiles(path.join(WEB, "components")).flatMap((f) =>
      visibleStrings(readFileSync(f, "utf8"), f).filter(({ text }) => /Continue with|Switch case|Reset demo|Yes, reset/i.test(text)),
    );
    expect(labels).toEqual([]);
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
