import { describe, expect, it } from "vitest";
import type { AccountState, Activity, Graph, GraphDiff } from "@/lib/api/types";
import { indexDiff } from "@/lib/view/diff";
import { activityTitle, fieldTitle, humanizeKey, humanValue, kindTag } from "@/lib/graph/labels";
import { buildExplorerModel, type ExplorerModel } from "@/lib/graph/model";
import { loadExample, loadFixture } from "./contract-validator";

const ids = {
  objection: "0c1aad00-0000-4000-8000-00000000ad27",
  commercial: "0c1aad00-0000-4000-8000-00000000ad25",
  owner: "0c1aad00-0000-4000-8000-00000000ad21",
  commitment: "0c1aad00-0000-4000-8000-00000000ad24",
  email13: "0ac7ad00-0000-4000-8000-00000000ad0d",
  email4: "0ac7ad00-0000-4000-8000-00000000ad04",
  quote: "0ac7ad00-0000-4000-8000-00000000ad03",
  contact: "0ac7ad00-0000-4000-8000-00000000ad02",
  signal: "051aad00-0000-4000-8000-00000000ad40",
};

const medtech = (): ExplorerModel =>
  buildExplorerModel({
    graph: loadFixture<Graph>("medtech.graph.json"),
    marks: indexDiff(loadFixture<GraphDiff>("medtech.graph-diff.json")),
    showMarks: true,
    activities: loadFixture<{ items: Activity[] }>("medtech.timeline.json").items,
    state: loadFixture<AccountState>("medtech.state.json"),
    eventId: "05e0ad00-0000-4000-8000-00000000ad0d",
  });

const acme = (): ExplorerModel =>
  buildExplorerModel({
    graph: loadFixture<Graph>("acme.graph.json"),
    marks: indexDiff(loadFixture<GraphDiff>("acme.graph-diff.json")),
    showMarks: true,
    activities: loadFixture<{ items: Activity[] }>("acme.timeline.json").items,
    state: loadExample<AccountState>("account_state"),
    eventId: "05e00000-0000-4000-8000-000000000101",
  });

describe("label helpers", () => {
  it("turns keys into words", () => {
    expect(humanizeKey("customer_replied")).toBe("Customer replied");
    expect(humanizeKey("EmailReceived")).toBe("Email received");
    expect(humanizeKey("CRMTaskLogged")).toBe("CRM task logged");
    expect(humanizeKey("")).toBe("");
  });

  it("names a field the way a seller would, singular for one item of a list", () => {
    expect(fieldTitle("objections")).toBe("Objection");
    expect(fieldTitle("decision_criteria")).toBe("Decision criterion");
    expect(fieldTitle("current_commitments")).toBe("Commitment");
    expect(fieldTitle("next_meeting")).toBe("Next meeting");
    expect(fieldTitle("health")).toBe("Health");
  });

  it("reads enum values as words and leaves prose alone", () => {
    expect(humanValue("at_risk")).toBe("at risk");
    expect(humanValue("medium")).toBe("medium");
    expect(humanValue("Weighing Quantum Circuits Inc.'s pricing")).toBe("Weighing Quantum Circuits Inc.'s pricing");
  });

  it("tags each kind with a plain word", () => {
    expect(kindTag("Opportunity")).toBe("Deal");
    expect(kindTag("Claim")).toBe("Fact");
    expect(kindTag("Conversation")).toBe("Activity");
    expect(kindTag("DecisionEpisode")).toBe("Decision");
    expect(kindTag("Mystery")).toBe("Mystery");
  });

  it("titles an activity by what happened and who said it", () => {
    const from = (type: string, who: Activity["participants"]): Activity => ({ activity_type: type, participants: who }) as Activity;
    const fatoumata = { raw_identity: "f@x", display_name: "Fatoumata Touré", role: "from", person_id: "p" } as Activity["participants"][number];
    expect(activityTitle("EmailReceived", from("EmailReceived", [fatoumata]))).toBe("Email from Fatoumata");
    expect(activityTitle("MeetingCompleted", from("MeetingCompleted", [{ ...fatoumata, role: "attendee" }]))).toBe("Meeting with Fatoumata");
    expect(activityTitle("ContactAdded", from("ContactAdded", [{ ...fatoumata, role: "mentioned" }]))).toBe("Contact added: Fatoumata Touré");
    expect(activityTitle("QuoteCreated", undefined)).toBe("Quote created");
    expect(activityTitle("SomethingNew", undefined)).toBe("Something new");
  });
});

describe("display labels in the model", () => {
  it("shows facts by field and value, emails by sender and day, signals in words", () => {
    const m = medtech();
    const label = (id: string) => m.byId.get(id)!.label;
    expect(label(ids.objection)).toBe("Objection: Long-term cost of integrating EduTech Lab and SecureData Nexus");
    expect(label(ids.commercial)).toBe("Commercial issue: Weighing Quantum Circuits Inc.'s initial pricing and onboarding");
    expect(label(ids.owner)).toBe("Owner: Luis Rodriguez");
    expect(label(ids.commitment)).toBe("Commitment: Fatoumata to gather her team's feature requests");
    expect(label(ids.email13)).toBe("Email from Fatoumata · Nov 9, 2023");
    expect(label(ids.email4)).toBe("Email from Luis · Oct 31, 2023");
    expect(label(ids.quote)).toBe("Quote created · Oct 15, 2023");
    expect(label(ids.contact)).toBe("Contact added: Fatoumata Touré · Oct 15, 2023");
    expect(label(ids.signal)).toBe("Customer replied");
    expect(m.byId.get(ids.objection)!.name).toBe("Objection: Long-term cost of integrating EduTech Lab and SecureData Nexus (Fact)");
    expect(m.byId.get(ids.objection)!.kindTag).toBe("Fact");
  });

  it("never renders a raw identifier as the head of a label", () => {
    const identifier = /\b[a-z]+_[a-z_]+\b|\b[A-Z][a-z]+[A-Z][A-Za-z]*\b|\b[A-Z]{2,}[a-z]+[A-Z]/;
    for (const m of [medtech(), acme()]) {
      // Names (the account, the deal, people) are what they are, CamelCase brands included; keys are not.
      for (const n of m.nodes.filter((x) => !["Account", "Opportunity", "Person"].includes(x.type))) {
        const head = n.label.split(/: | · /)[0]!;
        expect(head, n.label).not.toMatch(identifier);
      }
    }
  });

  it("keeps a fact's field as its label when the state has no value, in words", () => {
    const m = buildExplorerModel({ graph: loadFixture<Graph>("medtech.graph.json"), marks: indexDiff(null), showMarks: false, activities: [], state: null, eventId: null });
    expect(m.byId.get(ids.objection)!.label).toBe("Objection");
    expect(m.byId.get(ids.email13)!.label).toBe("Email received · Nov 9, 2023");
  });
});
