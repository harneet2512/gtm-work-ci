"""WP18 (HAR-116): semantic judges are offline evaluation, not an interactive call: they get their own provider
deadline (judge_deadline_s). The live run's 28 ProviderErrors were calls cut at the 90 s interactive deadline
(first attempt abandoned at 54 s, one 36 s retry)."""
from __future__ import annotations

from pathlib import Path

import pytest
from pydantic import ValidationError

from ghost_worker.llm.factory import build_judge_provider, build_provider
from ghost_worker.settings import Settings


def live(**overrides: object) -> Settings:
    return Settings(_env_file=None, ghost_llm_mode="live", openrouter_api_key="test-key-not-real", **overrides)


def test_judge_deadline_defaults_to_180_and_is_bounded() -> None:
    assert Settings(_env_file=None).judge_deadline_s == 180.0
    assert Settings(_env_file=None, judge_deadline_s=900).judge_deadline_s == 900
    for bad in (0, 901):
        with pytest.raises(ValidationError):
            Settings(_env_file=None, judge_deadline_s=bad)


def test_judge_provider_uses_the_judge_deadline_and_draft_keeps_the_llm_deadline() -> None:
    cfg = live(judge_deadline_s=200, llm_deadline_s=45)
    assert build_judge_provider(cfg).deadline_s == 200
    assert build_provider(cfg).deadline_s == 45


def test_env_example_documents_the_judge_deadline() -> None:
    lines = (Path(__file__).resolve().parents[2] / ".env.example").read_text(encoding="utf-8").splitlines()
    env = dict(line.split("=", 1) for line in lines if "=" in line and not line.startswith("#"))
    assert float(env["JUDGE_DEADLINE_S"]) == Settings(_env_file=None).judge_deadline_s
