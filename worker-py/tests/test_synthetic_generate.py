"""WP32 (HAR-131) step 2, precondition 4: the generation pipeline on fixtures/crmarena_sample.

Two runs are byte-identical with the same manifest hash; every event is a schema-valid SourceEvent whose
envelope (and only the envelope) carries origin=synthetic / provenance=synthetic:v1; synthetic ids never collide
with the base; deals with a visible real terminal outcome are never covered (Q2); planted features are rendered.
One event per rendered kind is pinned in fixtures/synthetic/generated_kinds.json for the Go normalisation test.
Regenerate after an intended change: SYNTH_REGEN=1 python -m pytest tests/test_synthetic_generate.py
"""
from __future__ import annotations

import json
import os
import sys
from functools import lru_cache
from pathlib import Path

import pytest
from jsonschema import Draft202012Validator
from referencing import Registry, Resource

ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(ROOT / "bench"))

from synthetic import generate, leakcheck, pipeline, rules  # noqa: E402

SAMPLE = ROOT / "fixtures" / "crmarena_sample"
KINDS = ROOT / "fixtures" / "synthetic" / "generated_kinds.json"
SCHEMAS = ROOT / "contracts" / "schemas"


def _files(out: Path) -> dict[str, bytes]:
    return {p.relative_to(out).as_posix(): p.read_bytes() for p in sorted(out.rglob("*")) if p.is_file()}


@lru_cache(maxsize=1)
def _run(tmp: str) -> tuple[dict, dict[str, bytes]]:
    out = Path(tmp)
    manifest = pipeline.generate_to(SAMPLE, out, rules.load_rules(), rules.load_doc())
    return manifest, _files(out)


@pytest.fixture(scope="module")
def generated(tmp_path_factory) -> tuple[dict, dict[str, bytes]]:
    return _run(str(tmp_path_factory.mktemp("gen_a")))


def _events(files: dict[str, bytes]) -> list[dict]:
    return [e for name, raw in files.items() if name.startswith("events/") for e in json.loads(raw)]


def _labels(files: dict[str, bytes], name: str) -> list[dict]:
    return [json.loads(line) for line in files[f"labels/{name}"].decode("utf-8").splitlines()]


def test_two_runs_are_byte_identical_with_the_same_manifest_hash(generated, tmp_path) -> None:
    manifest_a, files_a = generated
    manifest_b = pipeline.generate_to(SAMPLE, tmp_path, rules.load_rules(), rules.load_doc())
    files_b = _files(tmp_path)
    assert files_a == files_b
    assert manifest_a["manifest_sha256"] == manifest_b["manifest_sha256"]
    assert set(json.loads(files_a["manifest.json"])["files"]) == set(files_a) - {"manifest.json"}


def test_manifest_carries_seed_hashes_counts_and_attribution(generated) -> None:
    manifest, files = generated
    doc = rules.load_doc()
    assert manifest["generation_seed"] == doc["generation_seed"] and manifest["rules_sha256"] == rules.content_hash(doc)
    assert manifest["deals"] == len(_labels(files, "deals.jsonl")) == manifest["covered_deals"] + manifest["real_outcome_deals"]
    assert manifest["events"] == len(_events(files)) > 0
    assert "CC BY-NC 4.0" in manifest["attribution"] and "CRMArena-Pro" in manifest["attribution"]
    assert "generated_at" not in files["manifest.json"].decode("utf-8")  # nothing depends on the clock


@lru_cache(maxsize=1)
def _validators() -> tuple[Draft202012Validator, Draft202012Validator]:
    docs = {p.name: json.loads(p.read_text(encoding="utf-8")) for p in SCHEMAS.glob("*.json")}
    reg = Registry().with_resources([(d["$id"], Resource.from_contents(d)) for d in docs.values() if "$id" in d])
    return (Draft202012Validator(docs["source_event.v1.json"], registry=reg),
            Draft202012Validator(docs["source_payloads.v1.json"], registry=reg))


def test_every_event_is_a_valid_marked_source_event(generated) -> None:
    envelope, payloads = _validators()
    for e in _events(generated[1]):
        assert not list(envelope.iter_errors(e)), e["source_object_id"]
        assert not list(payloads.iter_errors(e["payload"])), e["source_object_id"]
        assert (e["origin"], e["provenance"]) == ("synthetic", "synthetic:v1")
        assert e["connector"] == "crmarena-loader" and e["connector_version"] == "wp31-v1"


def test_no_rule_text_or_visible_marker_in_generated_events(generated, tmp_path) -> None:
    for name, raw in generated[1].items():
        if name.startswith("events/"):
            (tmp_path / name).parent.mkdir(parents=True, exist_ok=True)
            (tmp_path / name).write_bytes(raw)
    assert leakcheck.scan_generated(tmp_path / "events", rules.load_rules()) == []


def test_provenance_index_lists_every_event_once_without_ground_truth(generated) -> None:
    files = generated[1]
    rows = [json.loads(line) for line in files["index/events.jsonl"].decode("utf-8").splitlines()]
    triples = sorted((r["source_system"], r["source_object_id"], r["source_event_key"]) for r in rows)
    events = sorted((e["source_system"], e["source_object_id"], e["source_event_key"]) for e in _events(files))
    assert triples == events and len(set(triples)) == len(triples)
    assert {(r["origin"], r["provenance"]) for r in rows} == {("synthetic", "synthetic:v1")}
    assert all(set(r) <= {"source_system", "source_object_id", "source_event_key", "occurred_at", "file", "account",
                          "deal", "origin", "provenance"} for r in rows)  # no outcome, role or feature


def test_synthetic_ids_are_unique_and_never_collide_with_the_base(generated) -> None:
    base_ids = {r["Id"] for p in SAMPLE.glob("*.json") for r in json.loads(p.read_text(encoding="utf-8"))}
    events = _events(generated[1])
    triples = [(e["source_system"], e["source_object_id"], e["source_event_key"]) for e in events]
    assert len(triples) == len(set(triples))
    minted = {e["payload"]["message_id"] for e in events if e["source_system"] == "email"}
    minted |= {e["source_object_id"].split(":", 1)[1] for e in events
               if e["source_system"] == "crm" and e["payload"].get("created")}
    assert minted and not minted & base_ids


def test_covered_deals_exclude_every_real_terminal_deal(generated) -> None:
    files = generated[1]
    covered = json.loads(files["labels/covered_deals.json"])
    rows = {r["deal"]: r for r in _labels(files, "deals.jsonl")}
    assert covered["provenance"] == "synthetic:v1"
    assert covered["deals"] == sorted(d for d, r in rows.items() if r["outcome_source"] == "synthetic")
    real = {d for d, r in rows.items() if r["outcome_source"] == "real"}
    assert real and not real & set(covered["deals"])
    for e in _events(files):  # Q2: a deal with a visible real outcome gets no synthetic stage path
        if e["payload"].get("object_type") == "Opportunity" and "StageName" in e["source_event_key"]:
            assert e["payload"]["record_id"].split(":", 1)[1] not in real


def test_planted_seller_actions_are_rendered(generated) -> None:
    files = generated[1]
    by_deal: dict[str, list[dict]] = {}
    for e in _events(files):
        by_deal.setdefault(e["payload"].get("crm_opportunity_ref") or e["payload"].get("opportunity_record_id")
                           or e["payload"].get("record_id"), []).append(e)
    for row in _labels(files, "deals.jsonl"):
        evs = by_deal.get(f"opp:{row['deal']}", [])
        if "syn1_cc_colleague_on_technical_reply" in row["features"]:
            assert any(e["payload"].get("cc") for e in evs), row["deal"]
        if "syn1_reprice_after_quote_pushback" in row["features"]:
            assert any(e["source_event_key"].startswith("field:Amount:") for e in evs), row["deal"]


def _kinds(files: dict[str, bytes]) -> list[dict]:
    seen: dict[str, dict] = {}
    for e in _events(files):
        p = e["payload"]
        key = "|".join((e["source_system"], p.get("object_type") or p["direction"],
                        ":".join(e["source_event_key"].split(":")[:2]), "cc" if p.get("cc") else ""))
        seen.setdefault(key, e)
    return [seen[k] for k in sorted(seen)]


def test_generated_kinds_fixture_is_current(generated) -> None:
    kinds = _kinds(generated[1])
    text = json.dumps(kinds, indent=1, sort_keys=True, ensure_ascii=False) + "\n"
    if os.environ.get("SYNTH_REGEN"):
        KINDS.write_text(text, encoding="utf-8", newline="\n")
    assert KINDS.read_text(encoding="utf-8") == text, "rerun with SYNTH_REGEN=1 after an intended change"
    assert {e["source_system"] for e in kinds} == {"email", "crm"}


def test_main_refuses_a_non_empty_out_directory(tmp_path, monkeypatch) -> None:
    monkeypatch.setattr(generate, "preflight", lambda: [])
    (tmp_path / "stale.json").write_text("{}", encoding="utf-8")
    assert generate.main(["--base", str(SAMPLE), "--out", str(tmp_path)]) == 2


def test_main_generates_when_preflight_passes(tmp_path, monkeypatch, capsys) -> None:
    monkeypatch.setattr(generate, "preflight", lambda: [])
    assert generate.main(["--base", str(SAMPLE), "--out", str(tmp_path / "out")]) == 0
    assert json.loads(capsys.readouterr().out)["deals"] > 0
