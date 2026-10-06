// What the graph holds against the record: activities against the loaded timeline, facts against the claims the
// account state cites. Said plainly under the canvas, so a sparse graph is never mistaken for a quiet account.
import type { Activity } from "@/lib/api/types";
import type { ClaimIndex } from "./claim-index";
import { isActivityType } from "./kinds";
import type { ExplorerModel } from "./model";

export interface Count {
  shown: number;
  total: number;
}

export interface Coverage {
  activities: Count;
  facts: Count;
  text: string;
}

const noun = (n: number, one: string, many: string): string => `${n} ${n === 1 ? one : many}`;

function part(c: Count, one: string, many: string, windowNote: boolean): string {
  if (c.total === 0) return noun(c.shown, one, many);
  if (c.shown > c.total) return windowNote ? `${noun(c.shown, one, many)} (${c.total} on record in this window)` : noun(c.shown, one, many);
  if (c.shown === c.total) return `all ${noun(c.total, one, many)}`;
  return `${c.shown} of ${noun(c.total, one, many)}`;
}

export function coverageOf(model: ExplorerModel, timeline: readonly Activity[], claims: ClaimIndex): Coverage {
  const activities = { shown: model.nodes.filter((n) => isActivityType(n.type)).length, total: timeline.length };
  const onRecord = [...claims.values()].filter((facts) => facts.some((f) => !f.outranked)).length;
  const facts = { shown: model.nodes.filter((n) => n.type === "Claim" || n.type === "Commitment").length, total: onRecord };
  if (activities.shown + activities.total + facts.shown + facts.total === 0) return { activities, facts, text: "No activities or facts are on record yet." };
  const a = part(activities, "activity", "activities", true);
  const f = part(facts, "fact", "facts", false);
  const onRecordWords = activities.total > 0 && facts.total > 0 && activities.shown <= activities.total ? " on record" : "";
  return { activities, facts, text: `The graph holds ${a} and ${f}${onRecordWords}.` };
}
