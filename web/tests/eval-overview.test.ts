// The eval overview (/evals): every eval type in the catalog and every eval of the registry, read from the contracts
// themselves, less what the registry marks hidden, with judge quality shown only when a recorded snapshot exists. The
// operational metrics are registered apart from the evals and are counted nowhere.
import { describe, expect, it } from "vitest";
import { buildOverview, contractsDirFromEnv, loadEvalContracts, type EvalQuality } from "@/lib/evals/registry";
import { CONTRACTS_DIR } from "./contract-validator";

const contracts = loadEvalContracts(CONTRACTS_DIR);

describe("loadEvalContracts", () => {
  it("reads the catalog, the registry and the surface names from the contracts directory", () => {
    expect(Object.keys(contracts.catalog.eval_types)).toHaveLength(39);
    expect(contracts.registry.evals).toHaveLength(207);
    expect(contracts.registry.metrics).toHaveLength(5);
    expect(contracts.matrix.surfaces).toHaveLength(20);
  });

  it("fails with a readable error when the contracts are missing", () => {
    expect(() => loadEvalContracts("/nowhere/contracts")).toThrow(/eval contracts could not be read from \/nowhere\/contracts/);
  });

  it("finds the contracts beside the web app unless GHOST_CONTRACTS_DIR says otherwise", () => {
    expect(contractsDirFromEnv({ GHOST_CONTRACTS_DIR: "/srv/contracts" }, "/app/web")).toBe("/srv/contracts");
    expect(contractsDirFromEnv({}, "/app/web").replaceAll("\\", "/")).toMatch(/\/app\/contracts$/);
  });
});

describe("buildOverview", () => {
  const overview = buildOverview(contracts, null);

  it("counts what exists, in words not scores", () => {
    expect(overview.totals).toEqual({ draftTypes: 35, registryEvals: 205, live: 39, partial: 42, planned: 124 });
    expect(overview.measured).toBe(false);
  });

  it("groups the draft checks by how proven they are, every judged catalog type exactly once (a planned type has no judge yet)", () => {
    expect(overview.groups.map((g) => g.tag.tag)).toEqual(["Policy rule", "Deal evidence", "Sales methodology", "CS practice"]);
    const types = overview.groups.flatMap((g) => g.rows.map((r) => r.evalType));
    expect(new Set(types).size).toBe(35);
  });

  it("describes one check with its plain name, grader, blocking rule, surfaces and honest quality", () => {
    const cta = overview.groups.flatMap((g) => g.rows).find((r) => r.evalType === "cta_calibration")!;
    expect(cta).toMatchObject({
      name: "CTA calibration",
      question: "Is the ask the right size for where the buyer is?",
      grader: "AI judge",
      canBlock: true,
      surfaces: ["Action/output generation"],
      quality: null,
    });
    expect(cta.blockingRule).toMatch(/^Block only when the ask is a closing commitment/);
    expect(cta.definition).toMatch(/^Size and type of the ask/);
    const unmapped = overview.groups.flatMap((g) => g.rows).find((r) => r.evalType === "buyer_readiness")!;
    expect(unmapped.surfaces).toEqual([]);
  });

  it("shows measured quality only for the evals a report covers", () => {
    const quality = new Map<string, EvalQuality>([["grounding", { agreement: 0.9, falsePass: 0.05, falseBlock: 0.02, cases: 40, report: "Recorded Oct 10, 2026" }]]);
    const measured = buildOverview(contracts, quality);
    const rows = measured.groups.flatMap((g) => g.rows);
    expect(measured.measured).toBe(true);
    expect(rows.find((r) => r.evalType === "grounding")!.quality).toMatchObject({ agreement: 0.9, cases: 40 });
    expect(rows.find((r) => r.evalType === "cta_calibration")!.quality).toBeNull();
  });

  it("files every eval family under one of the three eval jobs, and leaves the metrics out of all of them", () => {
    const byJob = (job: string) => overview.families.filter((f) => f.job === job).map((f) => f.id);
    expect(byJob("intelligence")).toEqual(["E1", "E2", "E3", "E4", "E5", "E6", "E7"]);
    expect(byJob("decision_loop")).toEqual(["E8", "E9", "E10", "E11", "E12", "E13", "E14", "E15", "E16", "E17"]);
    expect(byJob("system")).toEqual(["E18", "E19", "E20", "E21", "E22"]);
  });

  it("leaves the evals the registry marks hidden out of the web catalog and out of its counts", () => {
    const ids = overview.families.flatMap((f) => f.evals.map((e) => e.id));
    for (const hidden of ["E16.5", "E22.4"]) expect(ids).not.toContain(hidden);
    expect(ids).toContain("E16.4");
    expect(ids).toContain("E22.3");
    expect(overview.families.find((f) => f.id === "E16")!.counts).toEqual({ live: 0, partial: 3, planned: 4 });
    expect(overview.families.find((f) => f.id === "E22")!.evals).toHaveLength(9);
  });

  it("shows the knowledge family's invariant as the registry states it (the learning continuation, no arm comparison), and renames the learning surface", () => {
    const e16 = overview.families.find((f) => f.id === "E16")!;
    expect(e16.invariant).toBe(
      "Knowledge learned from an earlier episode is retrieved, judged applicable and used in a later episode where its situation recurs, and the later candidates follow it or say why they depart from it.",
    );
    expect(e16.invariant).not.toMatch(/B over A|C equals A/);
    const surfaces = overview.families.flatMap((f) => f.evals.flatMap((e) => e.surfaces));
    expect(surfaces).toContain("Continual learning and regression");
    expect(surfaces.join(" ")).not.toMatch(/negative transfer/i);
  });

  it("lists every eval family with its evals, status, grader and surfaces", () => {
    expect(overview.families).toHaveLength(22);
    const e19 = overview.families.find((f) => f.id === "E19")!;
    expect(e19.name).toBe("Grader validity");
    expect(e19.kind).toBe("Validation");
    expect(e19.evals.map((e) => e.id)).toContain("E19.3");
    const e11 = overview.families.find((f) => f.id === "E1")!.evals.find((e) => e.id === "E1.1")!;
    expect(e11).toEqual({ id: "E1.1", name: "Source grounding", status: "Live", grader: "Rule check", mode: "Blocks", surfaces: ["Source/evidence"] });
    const planned = overview.families.flatMap((f) => f.evals).find((e) => e.status === "Planned")!;
    expect(planned).toMatchObject({ grader: "Not built yet", mode: "Not built yet" });
  });
});
