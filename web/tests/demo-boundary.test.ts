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
  visible_trigger: {
    surface: string;
    control: string;
    core_endpoint: string;
    also: { surface: string; control: string; core_endpoint: string; requires_confirmation: boolean }[];
  };
  ask_cliff: {
    dm: boolean;
    mention_in_thread: boolean;
    channel_top_level_messages: string[];
    actions: string[];
    dry_run_only: string[];
    agent: {
      memory: { verbatim_turns: number; older_turns: string; stored_in: string };
      max_tool_calls: number;
      deadline_s: number;
      progress: string;
      confirmation_per_state_change: boolean;
      continues_after_confirmation: boolean;
      trace: string;
    };
  };
  forbidden_terms: Term[];
}
interface Hit {
  term: string;
  match: string;
}

const WEB = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const boundary = readJson<Boundary>(path.join(CONTRACTS_DIR, "demo", "boundary.v1.json"));
const terms = boundary.forbidden_terms.map((t) => ({ id: t.id, re: new RegExp(t.pattern, t.case_sensitive ? "g" : "gi") }));

// A kebab/dotted identifier that carries the old name ("ghost-pick" CSS class, "ghost.demo" storage key) or a bare
// lowercase "ghost" token (an internal kind tag) is never rendered, so it is not brand copy. Capitalised or
// sentence-position "Ghost" is always flagged.
const INTERNAL_GHOST_IDENTIFIER = /(?<![\w-])(?:[a-z0-9_]+[-.])+ghost(?![\w])|(?<![\w-])ghost(?:[-.][a-z0-9_]+)+(?![\w])|^ghost:?$|--[a-z0-9-]*ghost[a-z0-9-]*/g;

function scan(text: string): Hit[] {
  const copy = text.replace(INTERNAL_GHOST_IDENTIFIER, " ");
  return terms.flatMap(({ id, re }) => [...copy.matchAll(re)].map((m) => ({ term: id, match: m[0] })));
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

  it("catches the retired brand name in rendered copy but not internal identifiers", () => {
    const tsx = 'export const P = () => <h1>Ghost&apos;s pick</h1>; const t = "How Ghost checks its work"; const u = `Ghost drafted ${n} moves`;';
    const hits = visibleStrings(tsx, "seeded.tsx").flatMap((s) => scan(s.text)).map((h) => h.term);
    expect(hits).toEqual(expect.arrayContaining(["legacy_brand"]));
    for (const text of ["gtm_ai's pick", "GHOST_API_TOKEN", "ghost_worker", "ghost.local"]) {
      expect(scan(text).map((h) => h.term)).not.toContain("legacy_brand");
    }
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
// The registry no longer carries out-of-scope experiment wording: the E16 invariant is the learning-continuation proof,
// E16.5 and E22.4 are marked hidden (the web catalog lists and counts neither) and the "negative transfer" surface is
// reworded by the web catalog (lib/evals/registry.ts). The ratchet scans the rendered catalog, so it is empty, and it fails
// if drift ever returns.
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

  it("is true of the registry contract itself that the experiment wording is gone from the E16 invariants, and the out-of-scope evals are marked hidden", () => {
    const registry = readJson<{ families: { id: string; invariant: string | null }[]; evals: { id: string; family: string; invariant: string | null; status: string }[] }>(
      path.join(CONTRACTS_DIR, "evals", "eval_registry.json"),
    );
    expect(scan(registry.families.find((f) => f.id === "E16")!.invariant ?? "")).toEqual([]);
    expect(registry.evals.filter((e) => e.family === "E16").flatMap((e) => scan(e.invariant ?? ""))).toEqual([]);
    expect(registry.evals.filter((e) => e.status === "hidden").map((e) => e.id)).toEqual(["E16.5", "E22.4"]);
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

  it("has one other trigger, Cliff's play_next in Slack, behind a confirmation and on the same core route", () => {
    expect(boundary.visible_trigger.also).toEqual([
      {
        surface: "cliff_in_slack",
        control: "play_next",
        core_endpoint: boundary.visible_trigger.core_endpoint,
        requires_confirmation: true,
      },
    ]);
  });

  it("lets Ask Cliff answer in a DM and a mention thread, keeps the channel top level to M1 M2 M3, and sends and writes nothing", () => {
    const ask = boundary.ask_cliff;
    expect(ask.dm && ask.mention_in_thread).toBe(true);
    expect(ask.channel_top_level_messages).toEqual(["M1", "M2", "M3"]);
    expect(ask.actions).toEqual(["play_next", "demo_status"]);
    expect(ask.dry_run_only).toEqual(["draft_followup", "crm_update_preview"]);
    expect([...ask.actions, ...ask.dry_run_only].filter((a) => /send|write|crm_update$/.test(a))).toEqual([]);
  });

  it("holds Ask Cliff, as an agent, to 8 remembered turns, 12 tool calls in 120 s, in-place progress, a confirmation per state change and a trace", () => {
    const agent = boundary.ask_cliff.agent;
    expect(agent.memory).toEqual({ verbatim_turns: 8, older_turns: "summarised", stored_in: "core" });
    expect([agent.max_tool_calls, agent.deadline_s]).toEqual([12, 120]);
    expect(agent.progress).toBe("updates_the_thinking_message_in_place");
    expect(agent.confirmation_per_state_change && agent.continues_after_confirmation).toBe(true);
    expect(agent.trace).toBe("every_answer_links_its_trace");
  });

  it("is the only web trigger: no button of the audience app continues into, or switches to, another account or resets the demo", () => {
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
