"""WP18 (HAR-116): judges replay from committed cassettes (no network in tests). If a prompt, rubric or schema
changed, re-seed with `python scripts/seed_judge_cassettes.py`."""
from __future__ import annotations

import json
from pathlib import Path

import pytest

from judge_doubles import CATALOG, RUBRICS
from judge_situations import SITUATIONS, UNIT_RUN_ID
from test_contracts import validator

from ghost_worker.judges import run_suite
from ghost_worker.llm.fake_provider import FakeProvider, model_family
from ghost_worker.settings import Settings

UNIT = Path(__file__).resolve().parents[1] / "cassettes" / "judges" / "unit"
FAMILY = model_family(Settings(_env_file=None).ghost_model)
EVAL_RESULT = validator("eval_result")


@pytest.mark.parametrize("name", list(SITUATIONS))
def test_judges_replay_from_cassettes(name: str) -> None:
    situation = SITUATIONS[name]
    suite = run_suite(situation.context, UNIT_RUN_ID, lambda t: FakeProvider(UNIT, FAMILY), RUBRICS, CATALOG,
                      eval_types=list(situation.answers))
    assert all(o.ok for o in suite.outcomes), [o.error for o in suite.outcomes]
    got = {o.eval_type: (o.result["verdict"], o.result["label"], o.result["blocking"]) for o in suite.outcomes}
    assert got == situation.expected
    assert all(not list(EVAL_RESULT.iter_errors(o.eval_result())) for o in suite.outcomes)


def test_cassettes_are_hand_written_and_neutral() -> None:
    files = sorted(UNIT.glob("*.json"))
    assert len(files) == sum(len(s.answers) for s in SITUATIONS.values())
    for path in files:
        document = json.loads(path.read_text(encoding="utf-8"))
        assert document["hand_written"] is True
        text = path.read_text(encoding="utf-8").lower()
        assert not any(name in text for name in ("acme", "beta corp", "northstar", "priya", "marco"))
