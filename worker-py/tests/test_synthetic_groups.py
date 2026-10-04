"""WP32 (HAR-131) v1.1: the two rule groups.

eval_known rules name the eval / knowledge / transition that already encodes them. hidden rules
(scored for uplift) must be unknown to the platform: their feature names and key terms appear in no
eval catalog entry, rubric, knowledge item or prompt, and their audit names the nearest eval and
knowledge item with the reason each does not encode the rule (semantic-overlap protection)."""
from __future__ import annotations

import json
import sys
from pathlib import Path

import pytest

ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(ROOT / "bench"))

from synthetic import experiment, leakcheck, rules  # noqa: E402

DOC = rules.load_doc()
CATALOG = json.loads((ROOT / "contracts" / "evals" / "eval_catalog.json").read_text(encoding="utf-8"))
HIDDEN = [r for r in DOC["rules"] if r["group"] == "hidden_company_specific"]
EVAL_KNOWN = [r for r in DOC["rules"] if r["group"] == "eval_known"]
# Everything the agent, the evals or the knowledge store read: catalog, rubrics (evaluator versions),
# knowledge items (contract examples, offered knowledge in eval cases), prompts (worker code, cassettes).
SURFACES = [p for p in (ROOT / "contracts" / "evals", ROOT / "contracts" / "examples", ROOT / "fixtures" / "evals",
                        ROOT / "worker-py" / "ghost_worker", ROOT / "worker-py" / "cassettes",
                        ROOT / "worker-py" / "tests" / "knowledge_lessons.py") if p.exists()]
KNOWN_KNOWLEDGE = {k for k in experiment.seeded_knowledge().ids if k.startswith("K")}
ALLOWLIST = json.loads((ROOT / "bench" / "synthetic" / "term_allowlist.v1.json").read_text(encoding="utf-8"))["entries"]
# Rubric and prompt sources can never be allow-listed; neither can a judge's system prompt.
NEVER_ALLOWED = ("worker-py/ghost_worker/", "contracts/", "worker-py/tests/knowledge_lessons.py")


def _snippets(term_set: set[str]) -> dict[Path, list[str]]:
    out: dict[Path, list[str]] = {}
    for e in ALLOWLIST:
        if leakcheck.normalize(e["term"]) in term_set:
            out.setdefault(ROOT / e["file"], []).append(e["snippet"])
    return out


def _exists(ref: str) -> bool:
    kind, _, name = ref.partition(":")
    return {"eval": name in CATALOG["eval_types"] or name in DOC["pending_evals"],
            "knowledge": name in KNOWN_KNOWLEDGE,
            "transition": name in DOC["transition_facts"],
            "har129": name == "reorg_example",
            "case": any((ROOT / "fixtures" / "evals").rglob(f"{name}.json"))}[kind]


@pytest.mark.parametrize("rule", HIDDEN, ids=lambda r: r["key"])
def test_hidden_rule_terms_appear_on_no_eval_knowledge_or_prompt_surface(rule: dict) -> None:
    needles = {leakcheck.normalize(t) for t in rule["key_terms"]} | {leakcheck.normalize(rule["key"].removeprefix("syn1_"))}
    hits = leakcheck.scan(SURFACES, needles, allow=rule.get("observable_text", []), snippets=_snippets(needles))
    assert not hits, "\n".join(f"{p.relative_to(ROOT)}: {m!r}" for p, m in hits)


def _at(node: object, path: str) -> object:
    for part in path.strip(".").split("."):
        node = node[part]  # type: ignore[index]
    return node


@pytest.mark.parametrize("entry", ALLOWLIST, ids=lambda e: f"{e['file'][-12:]}:{e['term']}")
def test_allowlist_entries_are_exact_current_and_never_rubric_or_prompt(entry: dict) -> None:
    """Each allow-listed occurrence must exist exactly where recorded, contain its term, give a reason, and sit
    outside every rubric/prompt source and outside a judge's system prompt (request.system)."""
    assert not entry["file"].startswith(NEVER_ALLOWED) and not entry["json_path"].startswith(".request.system")
    assert leakcheck.normalize(entry["term"]) in entry["snippet"] and len(entry["reason"]) >= 40
    value = _at(json.loads((ROOT / entry["file"]).read_text(encoding="utf-8")), entry["json_path"])
    assert f" {entry['snippet']} " in f" {leakcheck.normalize(str(value))} ", "stale allowlist entry"


def test_an_allowlisted_snippet_is_blanked_only_in_its_own_file(tmp_path: Path) -> None:
    text = json.dumps({"note": "write to the operations person before the quote"})
    (tmp_path / "a.json").write_text(text, encoding="utf-8")
    (tmp_path / "b.json").write_text(text, encoding="utf-8")
    needles = {leakcheck.normalize("operations person")}
    snips = {tmp_path / "a.json": ["write to the operations person before the quote"]}
    assert {p.name for p, _ in leakcheck.scan([tmp_path], needles, snippets=snips)} == {"b.json"}


@pytest.mark.parametrize("rule", HIDDEN, ids=lambda r: r["key"])
def test_hidden_rule_audit_names_the_nearest_eval_and_knowledge(rule: dict) -> None:
    refs = [a["nearest"] for a in rule["audit"]]
    assert all(_exists(r) for r in refs), refs
    assert any(r.startswith("eval:") for r in refs) and any(r.startswith("knowledge:") for r in refs)


@pytest.mark.parametrize("rule", EVAL_KNOWN, ids=lambda r: r["key"])
def test_eval_known_rule_names_an_existing_encoder(rule: dict) -> None:
    assert rule["encoded_by"] and all(_exists(r) for r in rule["encoded_by"]), rule["encoded_by"]


def test_the_known_confounds_are_classified_eval_known() -> None:
    """PR #19 review: champion_continuity + K17/K09, timing_cadence and economic_buyer_coverage + K21
    already encode these, so they cannot count as learning from outcomes."""
    by_key = {r["key"]: r for r in DOC["rules"]}
    expect = {"syn1_entrant_with_champion": "knowledge:K17", "syn1_entrant_without_champion": "eval:champion_continuity",
              "syn1_written_handover_exempts": "knowledge:K09", "syn1_requested_pause_ignored": "eval:timing_cadence",
              "syn1_quote_before_economic_buyer": "eval:economic_buyer_coverage"}
    for key, ref in expect.items():
        assert by_key[key]["group"] == "eval_known" and ref in by_key[key]["encoded_by"], key


def test_rendered_activity_text_is_whitelisted_but_the_rule_is_not(tmp_path: Path) -> None:
    """'<id> Amount changed to X' is the observable feature (core's CRM summary), not the rule."""
    rule = next(r for r in HIDDEN if r["key"] == "syn1_reprice_after_quote_pushback")
    needles = {leakcheck.normalize(t) for t in rule["key_terms"]}
    (tmp_path / "activity.json").write_text(json.dumps({"summary": "006Wt000007BAMjIAO Amount changed to 120000"}), encoding="utf-8")
    assert not leakcheck.scan([tmp_path], needles, allow=rule["observable_text"])
    (tmp_path / "prompt.py").write_text('HINT = "an amount changed after the quote is a bad sign"', encoding="utf-8")
    assert {p.name for p, _ in leakcheck.scan([tmp_path], needles, allow=rule["observable_text"])} == {"prompt.py"}


def test_json_fields_are_scanned_separately_so_no_phrase_spans_a_field_boundary(tmp_path: Path) -> None:
    (tmp_path / "case.json").write_text(json.dumps({"why": "EU operations", "person_id": "x"}), encoding="utf-8")
    assert not leakcheck.scan([tmp_path], {leakcheck.normalize("operations person")})
    (tmp_path / "k.json").write_text(json.dumps({"title": "Ask the operations person first"}), encoding="utf-8")
    assert {p.name for p, _ in leakcheck.scan([tmp_path], {leakcheck.normalize("operations person")})} == {"k.json"}


def test_the_term_scan_would_catch_a_hidden_term_in_a_knowledge_item(tmp_path: Path) -> None:
    term = HIDDEN[0]["key_terms"][0]
    (tmp_path / "knowledge.json").write_text(json.dumps({"title": f"Watch the {term.upper()} on deals"}), encoding="utf-8")
    assert leakcheck.scan([tmp_path], {leakcheck.normalize(term)})
