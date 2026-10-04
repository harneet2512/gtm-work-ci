"""python -m bench.uplift: prepare | plan | run | verify.

  prepare  from the git-ignored data: learn from previous deals, choose situations, build the world pack (ghostctl
           abc-pack) and write bench/uplift/frozen/*. Run once; the outputs are committed.
  plan     the number of strategy generations (and an estimate of model calls) a run would make; no model is called.
  run      ghostctl abc-arms (real lifecycle, orchestrator and worker) then the report, blind pairs and key.
  verify   re-check a report against its contract and recompute every aggregate from its situations.
"""
from __future__ import annotations

import argparse
import json
import os
import shutil
import subprocess
import sys
import tempfile
import time
from concurrent.futures import ThreadPoolExecutor
from collections.abc import Mapping, Sequence
from datetime import datetime, timezone
from pathlib import Path

from . import blind, frozen, guard, learn, report
from .frozen import FROZEN, LEARNING_PATH, MANIFEST_PATH, PACK_PATH, ROOT, RUNTIME_PATH, SITUATIONS_PATH

DEFAULT_OUT = Path(__file__).resolve().parent / "reports"
GHOSTCTL = ["go", "run", "./cmd/ghostctl"]


def parse_plan(text: str | None) -> dict[tuple[str, str], int] | None:
    """'disc:price_pushback=8,disc:second_quote=8,exc:price_pushback=2' -> {(kind, point): n}."""
    if not text:
        return None
    kinds = {"disc": "discriminating", "exc": "exception", "ctl": "control"}
    out = {}
    for part in text.split(","):
        left, n = part.split("=")
        kind, point = left.split(":")
        out[(kinds[kind], point)] = int(n)
    return out


def generations(situations: Sequence[Mapping[str, object]], ids: Sequence[str], irrelevant: Mapping[str, bool] | None = None) -> dict[str, int]:
    """Strategy generations a run makes. Per situation: B and A (the knowledge-withheld counterfactual) in one run, plus C
    when something learned is irrelevant there (an upper bound before the matcher is consulted: discriminating only)."""
    chosen = [s for s in situations if s["id"] in set(ids)]
    kinds = {k: sum(1 for s in chosen if s["kind"] == k) for k in ("discriminating", "exception", "control")}
    return {"situations": len(chosen), **kinds,
            "generations_max": 3 * kinds["discriminating"] + 2 * (kinds["exception"] + kinds["control"])}


def cmd_plan(args: argparse.Namespace) -> int:
    situations = frozen.read_json(SITUATIONS_PATH)["situations"]
    ids = frozen.run_ids(situations, parse_plan(args.plan))
    g = generations(situations, ids)
    print(json.dumps({**g, "model_calls_per_generation_assumed": args.per_generation,
                      "model_calls_max": g["generations_max"] * args.per_generation}, indent=1))
    return 0


def env_value(env_file: Path | None, name: str) -> str | None:
    """One non-secret setting (the model id) from the env file or the environment; never a key."""
    if name in os.environ:
        return os.environ[name]
    if env_file and env_file.exists():
        for line in env_file.read_text(encoding="utf-8-sig").splitlines():
            k, _, v = line.strip().partition("=")
            if k.strip() == name:
                return v.strip().strip("\"'")
    return None


def runtime(mode: str, env_file: Path | None, pulls: int, ids: Sequence[str] = ()) -> dict[str, object]:
    """The model, planner budget and situations of the run. Recording takes the model from the environment and pins it,
    with the situation ids, in runtime.v1.json; replay reads the pin, because the model family is part of every
    cassette key and a replay must cover exactly what was recorded."""
    if mode == "record":
        model = env_value(env_file, "GHOST_MODEL")
        if not model:
            raise SystemExit("recording needs GHOST_MODEL in the environment or the env file")
        before = frozen.read_json(RUNTIME_PATH) if RUNTIME_PATH.exists() else {}
        if before.get("situation_ids") and (before.get("configured_model"), before.get("pulls")) != (model, pulls):
            raise SystemExit("cassettes were recorded with another model or planner budget: record into an empty cassette directory")
        pinned = {"version": "uplift_runtime.v1", "configured_model": model, "pulls": pulls,
                  "situation_ids": sorted(set(before.get("situation_ids") or []) | set(ids))}  # staged recordings accumulate
        frozen.write_json(RUNTIME_PATH, pinned)
        return pinned
    return frozen.read_json(RUNTIME_PATH)


def provenance(arms: Mapping[str, object], mode: str, rt: Mapping[str, object]) -> dict[str, object]:
    manifest = frozen.read_json(MANIFEST_PATH)
    configured = str(rt["configured_model"])
    cas = frozen.cassette_digest()
    return {"runtime_model": {"configured": configured, "family": configured.rsplit("/", 1)[-1].split(":", 1)[0],
                              "answered": frozen.answered_models()},
            "llm_mode": mode, "calls": {"generation_runs": arms["calls"]["generation_runs"], "recorded_llm_calls": cas["count"]},
            "seed": manifest["seed"], "cassettes": cas,
            "data": {"synthetic_manifest_sha256": manifest["synthetic_manifest_sha256"],
                     "base_export_sha256": manifest["base_export_sha256"], "rules_version": manifest["rules_version"],
                     "rules_sha256": manifest["rules_sha256"], "split_sha256": manifest["split_sha256"],
                     "pack_sha256": frozen.sha256_file(PACK_PATH), "learning_sha256": manifest["learning_sha256"]}}


def finish(arms: Mapping[str, object], situations: Sequence[Mapping[str, object]], learning: Mapping[str, object],
           rt: Mapping[str, object], mode: str, out: Path) -> int:
    """From the arms file to abc_report.json, pairs_blind.jsonl and pairs_key.json; refuses an invalid report."""
    ran = {s["id"] for s in arms["situations"]}
    chosen = [s for s in situations if s["id"] in ran]
    record = guard.check_store(arms["store"], learning, empty_before_learning=arms["empty_before_learning"])
    doc = report.build(arms=arms, pack={"situations": chosen}, learning=learning, store=arms["store"], guard=record,
                       provenance=provenance(arms, mode, rt), seed=frozen.SEED)
    problems = report.verify(doc)
    if problems:
        print("the report is not valid:\n  " + "\n  ".join(problems), file=sys.stderr)
        return 1
    pairs, key = blind.build_pairs(arms, {"situations": chosen}, frozen.SEED)
    bad = blind.validate(pairs, key)
    if bad:
        print("the blind pairs are not valid:\n  " + "\n  ".join(bad), file=sys.stderr)
        return 1
    frozen.write_json(out / "abc_report.json", doc, indent=2)
    blind.write(out, pairs, key)
    print(doc["headline"]["text"])
    return 0


def run_arms(args: argparse.Namespace, ids: Sequence[str], arms_path: Path, mode: str, cassettes: Path, rt: Mapping[str, object],
             extra: Sequence[str] = (), pythonpath: bool = False) -> int:
    cmd = [*args.ghostctl.split(), "abc-arms", "--pack", str(PACK_PATH), "--learning", str(LEARNING_PATH), "--out", str(arms_path),
           "--worker-mode", mode, "--cassettes", str(cassettes), "--only", ",".join(ids), "--pulls", str(rt["pulls"]),
           "--model", str(rt["configured_model"]), *extra]
    env = {**os.environ, "GHOST_WORKER_PYTHON": sys.executable}
    if pythonpath:
        env["PYTHONPATH"] = os.pathsep.join([str(ROOT), os.environ.get("PYTHONPATH", "")])
    done = subprocess.run(cmd, cwd=ROOT / "core-go", env=env, check=False)  # output carries names only, never a secret
    if done.returncode != 0:
        print("abc-arms failed; no report written", file=sys.stderr)
    return done.returncode


RECORD_RETRIES = 8


def drop_unusable(directory: Path) -> list[str]:
    """Delete recorded planner or drafter answers the worker's schema rejects (a malformed plan replays as the same
    failure for ever); the call is sampled again on the next recording. Returns the dropped file names."""
    sys.path.insert(0, str(ROOT / "worker-py"))
    from ghost_worker.strategies.schemas import StrategyArtifacts, StrategyPlan, parse_model_output

    dropped = []
    for f in sorted(directory.glob("*.json")) if directory.exists() else []:
        content = json.loads(f.read_text(encoding="utf-8")).get("response", {}).get("content")
        if not isinstance(content, dict):
            continue
        for model, key in ((StrategyPlan, "strategies"), (StrategyArtifacts, "artifacts")):
            if key in content:
                try:
                    parse_model_output(model, content, key)
                except Exception:  # noqa: BLE001 - any rejection makes it unusable
                    f.unlink()
                    dropped.append(f.name)
    return dropped


def newest(directory: Path) -> Path | None:
    files = [f for f in directory.glob("*.json")] if directory.exists() else []
    return max(files, key=lambda f: f.stat().st_mtime) if files else None


def record_shard(args: argparse.Namespace, ids: Sequence[str], shard: Path, rt: Mapping[str, object], extra: Sequence[str]) -> int:
    """Record one shard into its own cassette directory. A model answer that is unusable (malformed plan, a provider
    failure) aborts the process after it was recorded; it is the newest cassette, so it is dropped and that one call is
    sampled again (everything before it replays from its cassette). Each shard has its own directory so the newest file
    is always this shard's."""
    shard.mkdir(parents=True, exist_ok=True)
    code = 1
    for attempt in range(RECORD_RETRIES + 1):
        code = run_arms(args, ids, shard / "arms.json", "record", shard / "cassettes", rt, extra)
        if code == 0:
            return 0
        bad = newest(shard / "cassettes")
        if bad is None or time.time() - bad.stat().st_mtime > 600:
            return code
        print(f"shard {shard.name}: attempt {attempt + 1} failed; sampling the last call again", file=sys.stderr)
        bad.unlink()
    return code


def cmd_record_parallel(args: argparse.Namespace, ids: Sequence[str], rt: Mapping[str, object], extra: Sequence[str]) -> int:
    before = {f.name for f in frozen.CASSETTES.glob("*.json")} if frozen.CASSETTES.exists() else set()
    work = Path(tempfile.mkdtemp(prefix="abc_record_"))
    shards = [list(ids[i::args.workers]) for i in range(args.workers) if ids[i::args.workers]]
    dirs = []
    for i, part in enumerate(shards):
        d = work / f"shard{i}"
        shutil.copytree(frozen.CASSETTES, d / "cassettes") if frozen.CASSETTES.exists() else (d / "cassettes").mkdir(parents=True)
        dirs.append(d)
    with ThreadPoolExecutor(max_workers=len(shards)) as pool:
        codes = list(pool.map(lambda pair: record_shard(args, pair[0], pair[1], rt, extra), zip(shards, dirs)))
    frozen.CASSETTES.mkdir(parents=True, exist_ok=True)
    for d in dirs:  # every recorded call is kept, failed shards included: a rerun resumes from them
        for f in (d / "cassettes").glob("*.json"):
            if f.name not in before:  # never put back a cassette a shard dropped as unusable
                shutil.copy2(f, frozen.CASSETTES / f.name)
    shutil.rmtree(work, ignore_errors=True)
    return max(codes)


def cmd_run(args: argparse.Namespace) -> int:
    situations = frozen.read_json(SITUATIONS_PATH)["situations"]
    learning = frozen.read_json(LEARNING_PATH)
    ids = frozen.run_ids(situations, parse_plan(args.plan))
    out = Path(args.out_dir)
    out.mkdir(parents=True, exist_ok=True)
    rt = runtime(args.mode, Path(args.env_file) if args.env_file else None, args.pulls, ids)
    if args.mode == "replay" and not args.plan:
        ids = list(rt.get("situation_ids") or ids)  # a replay covers exactly what was recorded
    extra = ["--env-file", args.env_file] if args.env_file else []
    if args.mode == "record":
        dropped = drop_unusable(frozen.CASSETTES)
        if dropped:
            print(f"dropped {len(dropped)} unusable recorded answer(s); they are sampled again", file=sys.stderr)
    if args.mode == "record" and args.workers > 1:
        code = cmd_record_parallel(args, ids, rt, extra)
        if code:
            return code
        args.mode, rt = "replay", {**rt}  # the report is derived by replaying every recorded call (and proves they replay)
    code = run_arms(args, ids, out / "arms.json", args.mode, frozen.CASSETTES, rt, extra if args.mode == "record" else [])
    return code or finish(frozen.read_json(out / "arms.json"), situations, learning, rt, args.mode, out)


def cmd_rehearse(args: argparse.Namespace) -> int:
    """The whole stack on the real worlds with a scripted stand-in for the model, through to a validated report:
    zero live calls. The report goes to <out-dir>/rehearsal and is never a result."""
    situations = frozen.read_json(SITUATIONS_PATH)["situations"]
    learning = frozen.read_json(LEARNING_PATH)
    ids = frozen.run_ids(situations, parse_plan(args.plan))
    out = Path(args.out_dir) / "rehearsal"
    out.mkdir(parents=True, exist_ok=True)
    rt = {"configured_model": "openrouter/scripted/model", "pulls": args.pulls}
    code = run_arms(args, ids, out / "arms.json", "replay", out / "no_cassettes", rt, ["--worker-app", "bench.uplift.scripted:create_app"], True)
    return code or finish(frozen.read_json(out / "arms.json"), situations, learning, rt, "replay", out)


def cmd_verify(args: argparse.Namespace) -> int:
    problems = report.verify(json.loads(Path(args.report).read_text(encoding="utf-8")))
    print("\n".join(problems) if problems else "ok")
    return 1 if problems else 0


def remap(situations: Sequence[Mapping[str, object]], rows: Sequence[Mapping[str, object]], target: Mapping[str, str]) -> tuple[list[dict], list[dict]]:
    """Decide each candidate's final kind from the dry run (the matcher and the real lifecycle, no model): an item the
    lifecycle kept applies (discriminating) or is retrieved and does not (exception); when no learned item about the
    decision point survived, a discriminating candidate becomes a control (the retrieved knowledge is irrelevant to it)
    and an exception candidate has nothing to be an exception of. Everything else is excluded with its reason."""
    kept: list[dict] = []
    excluded: list[dict] = []
    by_id = {r["id"]: r for r in rows}
    for s in situations:
        row = by_id[s["id"]]
        labels = row.get("labels") or {}
        point_item = target.get(s["decision_point"])
        if not row.get("error"):
            kept.append(dict(s))
        elif s["kind"] == "discriminating" and point_item not in labels and "DOES_NOT_APPLY" in labels.values() \
                and "APPLIES" not in labels.values():
            kept.append({**s, "kind": "control", "selection_reason": s["selection_reason"] +
                         "; no learned item about this decision point survived the lifecycle, so the knowledge retrieved here is irrelevant to it"})
        else:
            excluded.append({"id": s["id"], "kind": s["kind"], "reason": row["error"]})
    return kept, excluded


def run_ghostctl(args: argparse.Namespace, *extra: str) -> None:
    subprocess.run([*args.ghostctl.split(), *extra], cwd=ROOT / "core-go", check=True, env={**os.environ, "GHOST_WORKER_PYTHON": sys.executable})


def cmd_prepare(args: argparse.Namespace) -> int:
    from . import data, select

    export, synthetic = Path(args.export), Path(args.synthetic)
    world = data.load_world(export)
    cutoff = datetime.strptime(world.split["cutoff"], "%Y-%m-%d").replace(tzinfo=timezone.utc)
    learning = learn.learn(data.previous_views(world), cutoff)
    found = select.candidates(world, synthetic, cutoff, select.windows_from_learning(learning))
    plan = {(k, p): 10_000 for k in ("discriminating", "exception") for p in ("price_pushback", "second_quote")}
    chosen = [s.as_json() for s in select.order(select.pick(found, plan), frozen.SEED)]
    frozen.write_json(LEARNING_PATH, learning)
    candidates = FROZEN / "candidates.tmp.json"
    frozen.write_json(candidates, chosen)
    run_ghostctl(args, "abc-pack", "--export", str(export), "--synthetic", str(synthetic), "--specs", str(candidates), "--out", str(PACK_PATH))
    plan_path = FROZEN / "plan.tmp.json"
    run_ghostctl(args, "abc-arms", "--dry-run", "--pack", str(PACK_PATH), "--learning", str(LEARNING_PATH), "--out", str(plan_path))
    target = {k["decision_point"]: k["id"] for k in learning["knowledge"]}
    kept, excluded = remap(chosen, frozen.read_json(plan_path)["plan"], target)
    frozen.write_json(SITUATIONS_PATH, {"version": "uplift_situations.v1", "situations": kept, "excluded": excluded})
    pack = frozen.read_json(PACK_PATH)
    kinds = {s["id"]: s["kind"] for s in kept}
    pack["situations"] = [{**s, "kind": kinds[s["id"]]} for s in pack["situations"] if s["id"] in kinds]
    frozen.write_json(PACK_PATH, pack, indent=None)
    candidates.unlink()
    plan_path.unlink()
    frozen.write_json(MANIFEST_PATH, frozen.manifest(world.base.export_sha256, frozen.sha256_file(synthetic / "manifest.json"),
                                                     data.SPLIT_PATH, learning))
    print(f"learning: {sum(h['promoted'] for h in learning['hypotheses'])} promoted of {len(learning['hypotheses'])}; "
          f"{len(kept)} situations kept, {len(excluded)} excluded; frozen in {FROZEN}")
    return 0


def main(argv: Sequence[str] | None = None) -> int:
    p = argparse.ArgumentParser(prog="bench.uplift", description=__doc__.splitlines()[0])
    sub = p.add_subparsers(dest="cmd", required=True)
    a = sub.add_parser("prepare")
    a.add_argument("--export", required=True)
    a.add_argument("--synthetic", required=True)
    a.add_argument("--ghostctl", default=" ".join(GHOSTCTL))
    a.set_defaults(fn=cmd_prepare)
    b = sub.add_parser("plan")
    b.add_argument("--plan")
    b.add_argument("--per-generation", type=int, default=5, help="assumed model calls per strategy generation (measure with a pilot)")
    b.set_defaults(fn=cmd_plan)
    c = sub.add_parser("run")
    c.add_argument("--mode", choices=("replay", "record"), default="replay")
    c.add_argument("--plan")
    c.add_argument("--env-file")
    c.add_argument("--out-dir", default=str(DEFAULT_OUT))
    c.add_argument("--pulls", type=int, default=4)
    c.add_argument("--workers", type=int, default=1, help="record in this many parallel shards (each with its own embedded Postgres)")
    c.add_argument("--ghostctl", default=" ".join(GHOSTCTL))
    c.set_defaults(fn=cmd_run)
    r = sub.add_parser("rehearse", help="the whole stack with a scripted stand-in for the model (zero live calls)")
    r.add_argument("--plan")
    r.add_argument("--out-dir", default=str(Path(os.environ.get("TEMP", ".")) / "abc_rehearsal"))
    r.add_argument("--pulls", type=int, default=4)
    r.add_argument("--ghostctl", default=" ".join(GHOSTCTL))
    r.set_defaults(fn=cmd_rehearse)
    d = sub.add_parser("verify")
    d.add_argument("report")
    d.set_defaults(fn=cmd_verify)
    args = p.parse_args(list(argv) if argv is not None else None)
    return args.fn(args)
