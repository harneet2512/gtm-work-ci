// Demo mode (?demo=1) is CSS-only emphasis keyed on <html data-demo>. A selector that names a class no component renders
// silently does nothing (the original rules targeted .traj-rail, .ep-rail .node and .loop-strip .phase, none of which
// exist), so every class in the demo block must be rendered somewhere.
import { readFileSync, readdirSync, statSync } from "node:fs";
import path from "node:path";
import { describe, expect, it } from "vitest";

const root = path.resolve(__dirname, "..");
const css = readFileSync(path.join(root, "app/styles/control.css"), "utf8");

const sources = (dir: string): string[] =>
  readdirSync(dir).flatMap((f) => {
    const p = path.join(dir, f);
    if (statSync(p).isDirectory()) return f === "node_modules" || f === ".next" ? [] : sources(p);
    return /\.tsx$/.test(f) ? [readFileSync(p, "utf8")] : [];
  });
const tsx = [...sources(path.join(root, "components")), ...sources(path.join(root, "app"))].join("\n");

describe("demo mode CSS", () => {
  const block = css.slice(css.indexOf("/* Demo mode"));
  const classes = [...new Set([...block.matchAll(/\.([a-zA-Z][\w-]*)/g)].map((m) => m[1]!))];

  it("has rules", () => {
    expect(classes.length).toBeGreaterThan(3);
  });

  it.each(classes)("targets a class a component renders: .%s", (cls) => {
    // Only a className value counts: a bare word like "phase" also appears as a property name.
    const inClassName = new RegExp(String.raw`className=(?:"[^"]*|\{`+"`"+String.raw`[^`+"`"+String.raw`]*)(?<![\w-])`+cls+String.raw`(?![\w-])`);
    expect(inClassName.test(tsx), `.${cls} is not rendered by any component`).toBe(true);
  });
});
