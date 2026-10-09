// "fit=pass, grounding=pass, cta=warn" is how a model gate stores its dimensions. A person reads it in words, worst first:
// "Next-step ask: warn. Fits the account state and grounded in evidence: pass." Text that is not a dimension list is returned as is.

const LABEL: Readonly<Record<string, string>> = {
  fit: "fits the account state",
  grounding: "grounded in evidence",
  cta: "next-step ask",
  recipients: "right recipients",
  recipient: "right recipients",
  distinct: "differs from the other options",
  differentiation: "differs from the other options",
  tone: "tone",
  timing: "timing",
  wording: "wording",
  risk: "risk",
  strategy: "strategy",
  stakeholder: "stakeholders",
};

const PAIR = /^([a-z][a-z0-9_]*)=(pass|warn|fail|unknown)$/;
const SEVERITY: Readonly<Record<string, number>> = { fail: 0, warn: 1, unknown: 2, pass: 3 };

const label = (key: string): string => LABEL[key] ?? key.replaceAll("_", " ");
const sentenceCase = (s: string): string => s.charAt(0).toUpperCase() + s.slice(1);
const joinWords = (xs: readonly string[]): string => (xs.length <= 1 ? (xs[0] ?? "") : `${xs.slice(0, -1).join(", ")} and ${xs.at(-1)}`);

export function humanizeObserved(raw: string): string {
  const parts = raw.split(",").map((p) => p.trim());
  const dims = parts.map((p) => PAIR.exec(p));
  if (parts.length === 0 || dims.some((d) => d === null)) return raw;
  const list = dims.map((d) => ({ key: d![1]!, verdict: d![2]! }));
  const worst = list.filter((d) => d.verdict !== "pass").sort((a, b) => SEVERITY[a.verdict]! - SEVERITY[b.verdict]!);
  const passing = list.filter((d) => d.verdict === "pass").map((d) => label(d.key));
  const sentences = [...worst.map((d) => `${sentenceCase(label(d.key))}: ${d.verdict}.`), ...(passing.length > 0 ? [`${sentenceCase(joinWords(passing))}: pass.`] : [])];
  return sentences.join(" ");
}
