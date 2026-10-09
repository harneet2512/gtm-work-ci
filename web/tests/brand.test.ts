// The product is gtm_ai: no string the web app can put on screen says "Ghost". The only exceptions are the
// phrases the shared eval-wording contract owns (contracts/evals/eval_wording.json, mirrored by
// lib/evals/vocabulary.ts and used by the Slack surface): they change with that contract, not here.
import { readdirSync, readFileSync } from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import ts from "typescript";
import { describe, expect, it } from "vitest";

const WEB = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const CONTRACT_OWNED = [/Ghost's pick/g, /Is Ghost allowed/g, /outside Ghost/g];
const OLD_BRAND = /\bGhost\b/;

function sourceFiles(dir: string): string[] {
  return readdirSync(dir, { withFileTypes: true }).flatMap((e) => {
    const p = path.join(dir, e.name);
    if (e.isDirectory()) return sourceFiles(p);
    return /\.(ts|tsx)$/.test(e.name) && !e.name.endsWith(".d.ts") ? [p] : [];
  });
}

/** String literals, template text and JSX text: everything a component can render. */
function visibleStrings(file: string): { line: number; text: string }[] {
  const source = readFileSync(file, "utf8");
  const sf = ts.createSourceFile(file, source, ts.ScriptTarget.Latest, true, file.endsWith(".tsx") ? ts.ScriptKind.TSX : ts.ScriptKind.TS);
  const out: { line: number; text: string }[] = [];
  const visit = (node: ts.Node): void => {
    if (ts.isImportDeclaration(node) || ts.isExportDeclaration(node)) return;
    if (ts.isStringLiteral(node) || ts.isNoSubstitutionTemplateLiteral(node) || ts.isTemplateHead(node) || ts.isTemplateMiddle(node) || ts.isTemplateTail(node) || ts.isJsxText(node)) {
      out.push({ line: sf.getLineAndCharacterOfPosition(node.getStart(sf)).line + 1, text: node.text });
    }
    ts.forEachChild(node, visit);
  };
  visit(sf);
  return out;
}

const SOURCES = [
  ...sourceFiles(path.join(WEB, "app")),
  ...sourceFiles(path.join(WEB, "components")),
  ...sourceFiles(path.join(WEB, "lib")).filter((f) => !f.includes(`${path.sep}api${path.sep}`) && !f.endsWith(`${path.sep}vocabulary.ts`)),
];

describe("brand", () => {
  it("no visible web string says Ghost (contract-owned eval phrases aside)", () => {
    const hits = SOURCES.flatMap((file) =>
      visibleStrings(file)
        .filter(({ text }) => OLD_BRAND.test(CONTRACT_OWNED.reduce((t, re) => t.replace(re, ""), text)))
        .map(({ line, text }) => `${path.relative(WEB, file)}:${line}: ${text.trim().slice(0, 80)}`),
    );
    expect(hits).toEqual([]);
  });

  it("names the product gtm_ai in the shell", () => {
    const layout = readFileSync(path.join(WEB, "app", "layout.tsx"), "utf8");
    expect(layout).toContain('title: "gtm_ai"');
  });
});
