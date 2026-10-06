"""The extract prompt's field taxonomy: every contract fieldPath is defined contrastively, and no
benchmark fixture text leaks into the prompt (examples are synthetic)."""
from __future__ import annotations

import json
import re
from collections.abc import Iterator

import pytest

from ghost_worker.extract.prompt import build_system_prompt
from ghost_worker.extract.taxonomy import FIELD_DEFINITIONS, FieldDefinition, render_field_guide
from test_contracts import CONTRACTS, SCHEMAS, load_json

_CLAIM_DEFS = load_json(SCHEMAS / "claim.v1.json")["$defs"]
# Paths the contract declares but the extractor does not emit yet (extract-v5, ADR-0012) are not in the prompt.
CONTRACT_FIELD_PATHS: list[str] = [p for p in _CLAIM_DEFS["fieldPath"]["enum"]
                                   if p not in _CLAIM_DEFS["fieldPathPendingExtraction"]["enum"]]
FIXTURES = CONTRACTS.parent / "fixtures"
BENCH_REPORTS = CONTRACTS.parent / "bench" / "reports"
SHINGLE_WORDS = 5

# Confusions observed in the live benchmark (bench/reports/live-extract-2026-10-02.json, `missed_as`):
# (gold field, field the model chose instead). The gold field's NOT clause must point at the other one.
LIVE_CONFUSIONS = [
    ("commitment", "next_milestone"),
    ("next_milestone", "commitment"),
    ("buying_group.member", "stakeholder_role"),
    ("stakeholder_role", "buying_group.member"),
    ("next_meeting", "delegation"),
    ("delegation", "next_meeting"),
    ("next_meeting", "next_milestone"),
    ("next_milestone", "next_meeting"),
    ("blockers", "decision_criteria"),
    ("blockers", "commercial_issue"),
    ("decision_criteria", "blockers"),
    ("commercial_issue", "blockers"),
    ("decision_process", "stakeholder_role"),
    ("decision_process", "economic_buyer"),
    ("decision_process", "blockers"),
    ("product_use_case", "commercial_issue"),
    ("commitment", "next_meeting"),
    # extract-v2 live run: the champion's hand-off email yielded delegation only, losing champion_status.
    ("champion_status", "delegation"),
    ("delegation", "champion_status"),
    # extract-v3 live run: a buyer's "still driving it on our side" filed as owner (perspective), and
    # champion continuity must not read as a hand-off.
    ("champion", "owner"),
    ("owner", "champion"),
    ("champion", "delegation"),
    ("delegation", "champion"),
]
# Pairs that rule 8 lets one sentence fill together: both definitions must say so instead of excluding each other.
EMIT_BOTH_PAIRS = [("buying_group.member", "stakeholder_role"), ("next_meeting", "next_milestone"),
                   ("champion_status", "delegation")]


def _definition(path: str) -> FieldDefinition:
    return next(d for d in FIELD_DEFINITIONS if d.path == path)


def _mentions(text: str, path: str) -> bool:
    return re.search(rf"(?<![\w.]){re.escape(path)}(?![\w.])", text) is not None


def test_definitions_cover_the_contract_enum_exactly_once() -> None:
    paths = [d.path for d in FIELD_DEFINITIONS]
    assert len(paths) == len(set(paths)), "duplicate definition"
    assert paths == CONTRACT_FIELD_PATHS  # same set, rendered in contract order


@pytest.mark.parametrize("path", CONTRACT_FIELD_PATHS)
def test_system_prompt_defines_every_contract_field_path(path: str) -> None:
    block = re.search(rf"^\[{re.escape(path)}\]\n  IS: (.+)\n  NOT: (.+)\n  EXAMPLE: (.+)$",
                      build_system_prompt(), re.M)
    assert block, f"no IS/NOT/EXAMPLE definition for {path}"
    assert all(len(part.strip()) > 10 for part in block.groups())


@pytest.mark.parametrize("path", CONTRACT_FIELD_PATHS)
def test_every_not_clause_redirects_to_another_field_path(path: str) -> None:
    others = [p for p in CONTRACT_FIELD_PATHS if p != path]
    assert any(_mentions(_definition(path).is_not, other) for other in others)


@pytest.mark.parametrize("path", CONTRACT_FIELD_PATHS)
def test_every_example_is_a_quoted_sentence_with_a_value(path: str) -> None:
    assert re.fullmatch(r'"[^"]+" -> value "[^"]+"(, role [a-z_]+)?', _definition(path).example)


@pytest.mark.parametrize(("gold", "chosen"), LIVE_CONFUSIONS, ids=[f"{g}-vs-{c}" for g, c in LIVE_CONFUSIONS])
def test_live_confusions_are_contrasted(gold: str, chosen: str) -> None:
    assert _mentions(_definition(gold).is_not, chosen)


@pytest.mark.parametrize(("first", "second"), EMIT_BOTH_PAIRS)
def test_rule_8_pairs_tell_the_model_to_emit_both(first: str, second: str) -> None:
    prompt = build_system_prompt()
    assert f"both {first} and {second}" in prompt or f"both {second} and {first}" in prompt
    for path, other in ((first, second), (second, first)):
        not_clause = _definition(path).is_not
        assert _mentions(not_clause, other) and "emit both" in not_clause


def test_champion_status_is_the_champions_own_engagement_and_delegation_is_who_to_work_with() -> None:
    assert "own engagement" in _definition("champion_status").means
    assert "work with instead" in _definition("delegation").means


def test_owner_is_seller_side_only_and_champion_covers_continuity() -> None:
    assert "seller" in _definition("owner").means and "on our side" in _definition("owner").is_not
    assert "still" in _definition("champion").means and "continuity" in _definition("champion").means


def test_commitment_excludes_meeting_and_invite_requests() -> None:
    assert "invite" in _definition("commitment").is_not


def test_field_guide_is_rendered_into_the_system_prompt() -> None:
    assert render_field_guide() in build_system_prompt()


def _strings(node: object) -> Iterator[str]:
    if isinstance(node, str):
        yield node
    elif isinstance(node, dict):
        for value in node.values():
            yield from _strings(value)
    elif isinstance(node, list):
        for value in node:
            yield from _strings(value)


def _fixture_strings() -> list[str]:
    """Every string in the benchmark fixtures, plus the gold facts the live benchmark reports quote.

    A report's `raw` section is model output, and a model may echo the prompt's own example wording into a
    claim value; that is the prompt leaking into the output, not the benchmark leaking into the prompt."""
    fixtures = [json.loads(p.read_text(encoding="utf-8")) for p in sorted(FIXTURES.rglob("*.json"))]
    gold_facts = [json.loads(p.read_text(encoding="utf-8")).get("facts", []) for p in sorted(BENCH_REPORTS.glob("*.json"))]
    return [s for doc in [*fixtures, *gold_facts] for s in _strings(doc)]


def _words(text: str) -> list[str]:
    return re.findall(r"[a-z0-9']+", text.lower())


def _shingles(text: str) -> set[tuple[str, ...]]:
    words = _words(text)
    return {tuple(words[i:i + SHINGLE_WORDS]) for i in range(len(words) - SHINGLE_WORDS + 1)}


def test_field_guide_shares_no_five_word_run_with_any_fixture() -> None:
    guide = _shingles(render_field_guide())
    leaked = {" ".join(s) for text in _fixture_strings() for s in _shingles(text) & guide}
    assert not leaked


def _fixture_names() -> set[str]:
    """Every word of a gold person's name, and the distinctive first word of a gold account's name."""
    names: set[str] = set()
    for path in (FIXTURES / "gold").glob("*/cp*.json"):
        for entity in load_json(path)["expected"]["entities"]:
            words = _words(entity["display_name"])
            if entity["kind"] == "person":
                names.update(words)
            elif entity["kind"] == "account":
                names.add(words[0])
    return names


def test_field_guide_uses_no_fixture_person_or_account_names() -> None:
    names = _fixture_names()
    assert names, "gold entities not found"
    assert not names & set(_words(render_field_guide()))
