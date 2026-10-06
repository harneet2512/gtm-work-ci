"""Text-level guards on the presenter scripts (PowerShell is not run in CI): a missing Slack token must stop setup and Start,
and nothing presenter-facing may carry the old brand or channel."""
from __future__ import annotations

from pathlib import Path

import pytest

REPO = Path(__file__).resolve().parents[2]
SCRIPTS = REPO / "scripts" / "demo"


def test_setup_check_verifies_both_slack_token_names() -> None:
    text = (SCRIPTS / "setup-local.ps1").read_text(encoding="utf-8")
    for name in ("SLACK_BOT_TOKEN", "SLACK_APP_TOKEN"):
        assert name in text
    assert "value not read" in text


def test_a_failed_start_does_not_open_the_page() -> None:
    text = (SCRIPTS / "start-demo.ps1").read_text(encoding="utf-8")
    failure = text.split("if ($code -ne 0) {", 1)[1].split("}", 1)[0]
    assert "Start-Process" not in failure and "exit $code" in failure


def _assert_no_stale_names(path: Path) -> None:
    text = path.read_text(encoding="utf-8")
    for stale in ("#ghost-demo", "GHOST DEMO", "Ghost Demo - "):
        if path.name == "create-shortcuts.ps1" and stale == "Ghost Demo - ":
            continue  # it deletes the old shortcuts by their old name
        assert stale not in text, f"{path.name} still says {stale!r}"


def test_presenter_scripts_carry_no_old_brand_or_channel() -> None:
    for path in SCRIPTS.glob("*.ps1"):
        _assert_no_stale_names(path)


@pytest.mark.parametrize("name", ["local.md", "codespace.md"])
def test_presenter_docs_carry_no_old_brand_or_channel(name: str) -> None:
    path = REPO / "docs" / "demo" / name
    if not path.exists():  # the public CI mirror strips docs/
        pytest.skip("docs are not in this checkout")
    _assert_no_stale_names(path)
