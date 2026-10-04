"""bench/uplift/cli + frozen + data: planning, kind remapping, provenance and the committed frozen inputs."""
from __future__ import annotations

import json
import sys
from datetime import datetime, timezone
from pathlib import Path
from types import SimpleNamespace

import pytest

ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(ROOT))

from bench.synthetic import leakcheck, rules  # noqa: E402
from bench.uplift import cli, data, frozen, learn  # noqa: E402
from bench.uplift.observe import Rec  # noqa: E402

SITS = [
    {"id": "d1", "kind": "discriminating", "decision_point": "price_pushback", "deal_id": "D1", "account_id": "A",
     "trigger": {"source_object_id": "o1", "source_event_key": "received", "occurred_at": "2023-11-10T10:00:00Z"}, "selection_reason": "r"},
    {"id": "d2", "kind": "discriminating", "decision_point": "second_quote", "deal_id": "D2", "account_id": "A",
     "trigger": {"source_object_id": "o2", "source_event_key": "received", "occurred_at": "2023-11-11T10:00:00Z"}, "selection_reason": "r"},
    {"id": "e1", "kind": "exception", "decision_point": "second_quote", "deal_id": "D3", "account_id": "A",
     "trigger": {"source_object_id": "o3", "source_event_key": "received", "occurred_at": "2023-11-12T10:00:00Z"}, "selection_reason": "r"},
    {"id": "x1", "kind": "exception", "decision_point": "price_pushback", "deal_id": "D4", "account_id": "A",
     "trigger": {"source_object_id": "o4", "source_event_key": "received", "occurred_at": "2023-11-13T10:00:00Z"}, "selection_reason": "r"},
]


def test_parse_plan_reads_kind_point_and_count() -> None:
    assert cli.parse_plan(None) is None
    assert cli.parse_plan("disc:price_pushback=8,exc:price_pushback=2") == {("discriminating", "price_pushback"): 8,
                                                                         ("exception", "price_pushback"): 2}


def test_generations_counts_two_per_run_and_a_third_for_a_discriminating_c() -> None:
    g = cli.generations(SITS, [s["id"] for s in SITS])
    assert g == {"situations": 4, "discriminating": 2, "exception": 2, "control": 0, "generations_max": 10}


def test_run_ids_takes_the_first_n_of_the_fixed_seeded_order_per_kind_and_point() -> None:
    all_ids = frozen.run_ids(SITS, None)
    assert all_ids == ["d1", "d2", "e1", "x1"]
    one = frozen.run_ids(SITS, {("discriminating", "price_pushback"): 1, ("exception", "second_quote"): 1})
    assert one == ["d1", "e1"] and one == frozen.run_ids(list(reversed(SITS)), {("discriminating", "price_pushback"): 1, ("exception", "second_quote"): 1})


def row(sid: str, labels: dict, error: str = "") -> dict:
    return {"id": sid, "labels": labels, "error": error}


def test_remap_keeps_valid_rows_turns_irrelevant_discriminating_ones_into_controls_and_excludes_the_rest() -> None:
    target = {"price_pushback": "K-PRICE"}  # the second-quote item did not survive the lifecycle
    rows = [row("d1", {"K-PRICE": "APPLIES"}), row("d2", {"K-PRICE": "DOES_NOT_APPLY"}, "selection error: item does not apply"),
            row("e1", {"K-PRICE": "DOES_NOT_APPLY"}, "selection error: must be retrieved"), row("x1", {"K-PRICE": "DOES_NOT_APPLY"})]
    kept, excluded = cli.remap(SITS, rows, target)
    assert [(s["id"], s["kind"]) for s in kept] == [("d1", "discriminating"), ("d2", "control"), ("x1", "exception")]
    assert "irrelevant to it" in kept[1]["selection_reason"]
    assert [e["id"] for e in excluded] == ["e1"] and "must be retrieved" in excluded[0]["reason"]


def test_a_discriminating_candidate_where_something_applies_is_never_a_control() -> None:
    rows = [row(s["id"], {"K-PRICE": "APPLIES", "K-OTHER": "DOES_NOT_APPLY"}, "selection error") for s in SITS]
    kept, excluded = cli.remap(SITS, rows, {"price_pushback": "K-PRICE"})
    assert kept == [] and len(excluded) == 4


def test_env_value_reads_the_model_from_the_environment_or_the_env_file_and_never_a_key(tmp_path: Path, monkeypatch) -> None:
    env = tmp_path / ".env"
    env.write_text("﻿OPENROUTER_API_KEY=sk-secret\nGHOST_MODEL=openrouter/qwen/qwen3.8-27b:free\n", encoding="utf-8")
    monkeypatch.delenv("GHOST_MODEL", raising=False)
    assert cli.env_value(env, "GHOST_MODEL") == "openrouter/qwen/qwen3.8-27b:free"
    assert cli.env_value(env, "MISSING") is None and cli.env_value(None, "GHOST_MODEL") is None
    monkeypatch.setenv("GHOST_MODEL", "from-env")
    assert cli.env_value(env, "GHOST_MODEL") == "from-env"


def test_plan_prints_the_generations_and_the_model_call_ceiling(capsys, monkeypatch) -> None:
    monkeypatch.setattr(frozen, "read_json", lambda path: {"situations": SITS})
    assert cli.main(["plan", "--per-generation", "4"]) == 0
    out = json.loads(capsys.readouterr().out)
    assert out["generations_max"] == 10 and out["model_calls_max"] == 40


def test_view_of_carries_no_rule_key_and_only_observable_facts() -> None:
    bd = SimpleNamespace(deal=SimpleNamespace(account_id="A", contacts=[SimpleNamespace(id="c1", function="operations")],
                                              outbound=[SimpleNamespace(t=2.0, recipients=("c1",))]),
                         start=datetime(2022, 1, 3, tzinfo=timezone.utc))
    res = SimpleNamespace(deal_id="D1", records=(SimpleNamespace(t=3.0, kind="quote_sent", data=(("separate", True),)),),
                          close_t=40.0, won=True, features=(("syn1_hidden_rule", 3.0),))
    view = data.view_of(bd, res)
    assert view.contacts == {"c1": "operations"} and view.base_outbound == ((2.0, ("c1",)),)
    assert view.records == (Rec(3.0, "quote_sent", {"separate": True}),)
    assert "syn1" not in repr(view)


def test_recording_pins_the_model_planner_budget_and_situations_and_staged_recordings_accumulate(tmp_path: Path, monkeypatch) -> None:
    monkeypatch.setattr(cli, "RUNTIME_PATH", tmp_path / "runtime.v1.json")
    monkeypatch.setenv("GHOST_MODEL", "openrouter/qwen/qwen3.8-27b:free")
    first = cli.runtime("record", None, 4, ["b", "a"])
    assert first == {"version": "uplift_runtime.v1", "configured_model": "openrouter/qwen/qwen3.8-27b:free", "pulls": 4,
                     "situation_ids": ["a", "b"]}
    assert cli.runtime("record", None, 4, ["c"])["situation_ids"] == ["a", "b", "c"]
    assert cli.runtime("replay", None, 99)["situation_ids"] == ["a", "b", "c"]  # replay reads the pin, whatever it is asked
    with pytest.raises(SystemExit):
        cli.runtime("record", None, 6, ["d"])  # another planner budget would change every prompt
    monkeypatch.setenv("GHOST_MODEL", "other/model")
    with pytest.raises(SystemExit):
        cli.runtime("record", None, 4, ["d"])
    monkeypatch.delenv("GHOST_MODEL")
    (tmp_path / "runtime.v1.json").unlink()
    with pytest.raises(SystemExit):
        cli.runtime("record", None, 4, [])  # no model to record with


def test_a_replay_covers_exactly_the_recorded_situations_and_passes_the_pinned_model(tmp_path: Path, monkeypatch) -> None:
    monkeypatch.setattr(cli, "RUNTIME_PATH", tmp_path / "runtime.v1.json")
    frozen.write_json(tmp_path / "runtime.v1.json", {"version": "uplift_runtime.v1", "configured_model": "m/x:free", "pulls": 4,
                                                     "situation_ids": ["s2", "s1"]})
    monkeypatch.setattr(frozen, "read_json", lambda path, real=frozen.read_json: {"situations": SITS} if path == cli.SITUATIONS_PATH else real(path))
    seen: dict = {}

    def fake_run(cmd, **kw):  # the Go half writes the arms file; here an empty one
        seen["cmd"] = cmd
        Path(cmd[cmd.index("--out") + 1]).write_text("{}", encoding="utf-8")
        return SimpleNamespace(returncode=0)

    monkeypatch.setattr(cli.subprocess, "run", fake_run)
    monkeypatch.setattr(cli, "finish", lambda *a: seen.update(finish=a) or 0)
    assert cli.main(["run", "--mode", "replay", "--out-dir", str(tmp_path)]) == 0
    cmd = seen["cmd"]
    assert cmd[cmd.index("--only") + 1] == "s2,s1" and cmd[cmd.index("--model") + 1] == "m/x:free"
    assert cmd[cmd.index("--worker-mode") + 1] == "replay" and "--env-file" not in cmd
    assert seen["finish"][4] == "replay"


def test_a_failed_arms_run_writes_no_report(tmp_path: Path, monkeypatch, capsys) -> None:
    monkeypatch.setattr(cli, "RUNTIME_PATH", tmp_path / "runtime.v1.json")
    frozen.write_json(tmp_path / "runtime.v1.json", {"configured_model": "m", "pulls": 4, "situation_ids": []})
    monkeypatch.setattr(cli.subprocess, "run", lambda cmd, **kw: SimpleNamespace(returncode=3))
    called = []
    monkeypatch.setattr(cli, "finish", lambda *a: called.append(a) or 0)
    assert cli.main(["run", "--mode", "replay", "--out-dir", str(tmp_path)]) == 3
    assert not called and "no report written" in capsys.readouterr().err


def test_provenance_pins_the_model_the_cassettes_and_every_data_hash() -> None:
    if not frozen.MANIFEST_PATH.exists():
        pytest.skip("frozen inputs are not committed yet")
    prov = cli.provenance({"calls": {"generation_runs": 7}}, "replay", {"configured_model": "openrouter/qwen/qwen3.8-27b:free"})
    assert prov["runtime_model"]["family"] == "qwen3.8-27b" and prov["llm_mode"] == "replay"
    assert prov["calls"]["generation_runs"] == 7 and set(prov["data"]) >= {"pack_sha256", "learning_sha256", "rules_sha256"}
    assert prov["cassettes"] == frozen.cassette_digest()


def test_unusable_recorded_answers_are_dropped_and_good_ones_kept(tmp_path: Path) -> None:
    good = {"response": {"content": {"artifacts": []}}}
    bad = {"response": {"content": {"strategies": [{"title": "x"}]}}}  # a malformed plan replays as the same failure for ever
    other = {"response": {"content": {"done": True}}}
    for name, doc in (("good", good), ("bad", bad), ("other", other)):
        (tmp_path / f"{name}.json").write_text(json.dumps(doc), encoding="utf-8")
    assert cli.drop_unusable(tmp_path) == ["bad.json"]
    assert sorted(p.name for p in tmp_path.glob("*.json")) == ["good.json", "other.json"]
    assert cli.drop_unusable(tmp_path / "missing") == []


# ---- the committed frozen inputs -----------------------------------------------------------------------------------


def frozen_ready() -> bool:
    return all(p.exists() for p in (frozen.LEARNING_PATH, frozen.SITUATIONS_PATH, frozen.PACK_PATH, frozen.MANIFEST_PATH))


needs_frozen = pytest.mark.skipif(not frozen_ready(), reason="frozen inputs are not committed yet")


@needs_frozen
def test_the_manifest_pins_every_input_by_hash() -> None:
    m = frozen.read_json(frozen.MANIFEST_PATH)
    assert m["learning_sha256"] == learn.canonical_sha256(frozen.read_json(frozen.LEARNING_PATH))
    doc = rules.load_doc()
    assert (m["rules_sha256"], m["rules_version"]) == (rules.content_hash(doc), doc["rule_set_version"])
    assert m["split_sha256"] == frozen.sha256_file(data.SPLIT_PATH) and m["seed"] == frozen.SEED
    assert all(len(m[k]) == 64 for k in ("base_export_sha256", "synthetic_manifest_sha256", "split_sha256"))


@needs_frozen
def test_no_planted_rule_text_is_in_the_frozen_inputs_or_the_cassettes() -> None:
    roots = [frozen.FROZEN] + ([frozen.CASSETTES] if frozen.CASSETTES.exists() else [])
    hits = leakcheck.scan(roots, leakcheck.markers(rules.load_rules()))
    assert not hits, "\n".join(f"{p}: {m!r}" for p, m in hits)


@needs_frozen
def test_learning_uses_nothing_dated_on_or_after_the_cutoff_and_cites_only_previous_episodes() -> None:
    learning = frozen.read_json(frozen.LEARNING_PATH)
    cutoff = learning["cutoff"]
    previous = set(learning["previous_episode_ids"])
    assert cutoff == "2023-11-01T00:00:00Z"
    for k in learning["knowledge"]:
        assert k["created_at"] == cutoff and set(k["supporting_episode_ids"]) <= previous
        assert all(e["at"] < cutoff for e in k["evidence"])
        assert not [e for e in k["evidence"] if e["kind"] == "counterexample"]
    assert {h["decision_point"] for h in learning["hypotheses"] if h["promoted"]} == {k["decision_point"] for k in learning["knowledge"]}


@needs_frozen
def test_every_situation_is_in_the_pack_with_its_trigger_last_and_nothing_after_it() -> None:
    sits = frozen.read_json(frozen.SITUATIONS_PATH)
    pack = {s["id"]: s for s in frozen.read_json(frozen.PACK_PATH)["situations"]}
    assert {s["id"] for s in sits["situations"]} == set(pack)
    assert {s["kind"] for s in sits["situations"]} <= {"discriminating", "exception", "control"}
    learning = frozen.read_json(frozen.LEARNING_PATH)
    for s in sits["situations"]:
        p = pack[s["id"]]
        assert p["kind"] == s["kind"] and p["trigger"] == s["trigger"]
        last = p["events"][-1]
        assert (last["source_object_id"], last["source_event_key"]) == (s["trigger"]["source_object_id"], s["trigger"]["source_event_key"])
        assert all(e["occurred_at"] <= s["trigger"]["occurred_at"] for e in p["events"])
        assert s["trigger"]["occurred_at"] > learning["cutoff"]
    assert all(e["reason"] for e in sits["excluded"])
    text = json.dumps(sits)
    assert '"won"' not in text and "outcome" not in text.replace("outcome_source", "")


@needs_frozen
def test_the_pack_never_names_a_synthetic_marker_or_rule_to_the_agent() -> None:
    for s in frozen.read_json(frozen.PACK_PATH)["situations"]:
        for e in s["events"]:
            visible = {k: v for k, v in e.items() if k not in ("origin", "provenance")}
            assert not leakcheck.marker_leaks(visible), (s["id"], e["source_object_id"])
