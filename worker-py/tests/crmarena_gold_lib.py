"""Loaders and validators for the CRMArena gold (HAR-114 gold v2, part B): the cases, their schema, the generator package."""
from __future__ import annotations

import sys
from pathlib import Path

from jsonschema import Draft202012Validator, FormatChecker
from referencing import Resource

from eval_cases_lib import CASE_SCHEMA, KNOWLEDGE_VALIDATOR, OUTPUT_VALIDATOR
from test_contracts import REGISTRY, load_json
from test_fixtures import GOLD_SCHEMA

ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(ROOT / "bench" / "evals"))

CR = ROOT / "fixtures" / "evals" / "crmarena"
CR_SCHEMA = load_json(CR / "case.schema.json")
CR_FILES = sorted(CR.glob("cases/*/*.json"))
CR_CASES = {p.stem: load_json(p) for p in CR_FILES}
LABELS = ROOT / "bench" / "labels" / "crmarena"


def _validator() -> Draft202012Validator:
    Draft202012Validator.check_schema(CR_SCHEMA)
    registry = REGISTRY.with_resources([(GOLD_SCHEMA["$id"], Resource.from_contents(GOLD_SCHEMA)),
                                        (CASE_SCHEMA["$id"], Resource.from_contents(CASE_SCHEMA)),
                                        (CR_SCHEMA["$id"], Resource.from_contents(CR_SCHEMA))])
    return Draft202012Validator(CR_SCHEMA, registry=registry, format_checker=FormatChecker())


CR_VALIDATOR = _validator()
__all__ = ["CR", "CR_CASES", "CR_FILES", "CR_SCHEMA", "CR_VALIDATOR", "KNOWLEDGE_VALIDATOR", "LABELS", "OUTPUT_VALIDATOR", "ROOT"]
