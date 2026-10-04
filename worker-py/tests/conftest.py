"""Shared fixtures: sample extract requests."""
from __future__ import annotations

import copy
import json
from pathlib import Path

import pytest

FIXTURES = Path(__file__).resolve().parent / "fixtures"


def load_fixture(name: str) -> dict:
    return json.loads((FIXTURES / name).read_text(encoding="utf-8"))


@pytest.fixture
def email_request() -> dict:
    return copy.deepcopy(load_fixture("extract_email_request.json"))


@pytest.fixture
def transcript_request() -> dict:
    return copy.deepcopy(load_fixture("extract_transcript_request.json"))


@pytest.fixture
def injection_request() -> dict:
    return copy.deepcopy(load_fixture("extract_injection_request.json"))
