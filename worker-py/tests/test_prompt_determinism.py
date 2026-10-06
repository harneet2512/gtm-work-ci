"""B-1: a prompt built twice from equivalent inputs that differ only in run-scoped ids (and id-sorted order) must
give the same cassette key, or every rehearsal misses the recording and calls a live model."""
from __future__ import annotations

import copy
import uuid
from pathlib import Path
from typing import Any

import pytest
from ghost_worker.bucket2.decision_judge import JudgeRequest, judge
from ghost_worker.extract.prompt import choose_nonce
from ghost_worker.llm.cache_provider import CachingProvider, canonicalize
from ghost_worker.llm.provider import LLMResult
from ghost_worker.models.strategies import StrategiesRequest
from ghost_worker.strategies.prompt import build_planner_user_prompt
from strategy_doubles import strategies_request

STRATS = ("act", "wait", "ask")
PERSON = "0e7a1000-0000-4000-8000-0000000000b1"


def _uid(seed: str) -> str:
    return str(uuid.uuid5(uuid.NAMESPACE_URL, seed))


def _candidate(cid: str, stype: str) -> dict[str, Any]:
    return {"candidate_id": cid, "strategy_type": stype, "action_class": stype.upper(), "rationale": "r " + stype,
            "to": [{"person_id": PERSON, "why": "asked"}]}


def _request(run: str, kind: str, order: tuple[int, ...] = (0, 1, 2)) -> JudgeRequest:
    ids = {s: _uid(f"{run}:{s}") for s in STRATS}
    cands = [_candidate(ids[STRATS[i]], STRATS[i]) for i in order]
    evidence = sorted(f"candidate:{ids[s]}" for s in STRATS)  # the Go caller sorted raw uuids
    evidence += [_uid(f"{run}:act-evidence")]
    return JudgeRequest(kind=kind, subject_id=_uid(f"{run}:set"), payload={"candidates": cands, "state": {"t": "x"}},
                        evidence_ids=evidence)


class Recorder:
    """Upstream that answers with no dimensions: only the cassette key matters here."""

    def complete_json(self, *, system: str, user: str, schema: dict[str, Any], schema_name: str) -> LLMResult:
        return LLMResult(content={"dimensions": [], "summary": "s"}, model="m", usage={})


def _keys(tmp: Path, req: JudgeRequest) -> set[str]:
    judge(req, CachingProvider(Recorder(), tmp, "m"))
    return {p.stem for p in tmp.glob("*.json") if p.name != "cache-stats.json"}


@pytest.mark.parametrize("kind", ["set_quality", "intent_fit", "ranking"])
def test_decision_judge_cassette_key_ignores_run_ids_and_input_order(tmp_path: Path, kind: str) -> None:
    first = _keys(tmp_path / "a", _request("run-1", kind))
    second = _keys(tmp_path / "b", _request("run-2", kind, order=(2, 0, 1)))
    assert len(first) == 1 and first == second


def test_decision_judge_key_changes_when_candidate_content_changes(tmp_path: Path) -> None:
    base = _request("run-1", "set_quality")
    cands = copy.deepcopy(base.payload["candidates"])
    cands[0]["rationale"] = "different"
    other = base.model_copy(update={"payload": {**base.payload, "candidates": cands}})
    assert _keys(tmp_path / "a", base) != _keys(tmp_path / "b", other)


def test_planner_prompt_is_stable_across_run_ids() -> None:
    def prompt(run: str) -> str:
        body = strategies_request()
        body["run_id"], body["decision_episode_id"] = _uid(run), _uid(run + "ep")
        return build_planner_user_prompt(StrategiesRequest.model_validate(body))

    assert canonicalize(prompt("r1"), [])[0] == canonicalize(prompt("r2"), [])[0]


def test_judge_context_nonce_does_not_change_the_cassette_key() -> None:
    nonce_a, nonce_b = choose_nonce("run-1", "body-a"), choose_nonce("run-2", "body-a")
    assert nonce_a != nonce_b  # the prompt's own nonce does vary with the run id ...

    def prompt(nonce: str) -> str:
        return f"Context\n<<<CONTEXT-{nonce}\nbody\nCONTEXT-{nonce}>>>"

    assert canonicalize(prompt(nonce_a), [])[0] == canonicalize(prompt(nonce_b), [])[0]  # ... the key does not


def test_a_real_planner_prompt_differs_only_in_nonce_and_ids_between_runs() -> None:
    def prompt(run: str) -> str:
        body = strategies_request()
        body["run_id"], body["decision_episode_id"] = _uid(run), _uid(run + "ep")
        return build_planner_user_prompt(StrategiesRequest.model_validate(body))

    assert prompt("r1") != prompt("r2")
