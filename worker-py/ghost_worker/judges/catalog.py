"""The eval catalog (contracts/evals/eval_catalog.json): the one vocabulary the semantic judges read.

Labels, evidence class, can_block / blocking_rule and label_rule come from here, never from judge code. The
diagnostic vocabulary is the EvalResult contract's `diagnostic` enum; workflows are AgentRun's `workflow` enum.
"""
from __future__ import annotations

import json
from collections.abc import Mapping
from pathlib import Path
from types import MappingProxyType
from typing import Any, Literal

from pydantic import BaseModel, ConfigDict, ValidationError

from ..errors import ConfigError

REPO_ROOT = Path(__file__).resolve().parents[3]
CONTRACTS = REPO_ROOT / "contracts"
CATALOG_PATH = CONTRACTS / "evals" / "eval_catalog.json"
SCHEMAS = CONTRACTS / "schemas"


class CatalogEntry(BaseModel):
    """One eval type as the catalog defines it."""

    model_config = ConfigDict(frozen=True, extra="forbid")

    eval_type: str
    kind: str
    evidence_class: str
    labels: tuple[str, ...]
    can_block: bool
    har97_sections: tuple[str, ...]
    description: str
    blocking_rule: str | None = None
    label_rule: str | None = None
    status: Literal["planned"] | None = None  # in the vocabulary, but no judge emits it yet


class EvalCatalog(BaseModel):
    model_config = ConfigDict(frozen=True, arbitrary_types_allowed=True)

    version: int
    evidence_classes: Mapping[str, str]
    entries: Mapping[str, CatalogEntry]
    diagnostics: tuple[str, ...]
    workflows: tuple[str, ...]

    def entry(self, eval_type: str) -> CatalogEntry:
        try:
            return self.entries[eval_type]
        except KeyError:
            raise ConfigError(f"eval type {eval_type!r} is not in the eval catalog") from None

    def semantic_types(self) -> tuple[str, ...]:
        """Semantic eval types a judge exists for, in catalog order (the order of eval_result#/$defs/evalType). A type
        marked planned is in the vocabulary but has no rubric yet."""
        return tuple(name for name, e in self.entries.items() if e.kind == "semantic" and e.status is None)

    def order(self, eval_types: set[str] | frozenset[str]) -> tuple[str, ...]:
        """The given eval types in catalog order."""
        return tuple(name for name in self.entries if name in eval_types)


def _read(path: Path) -> dict[str, Any]:
    try:
        return json.loads(path.read_text(encoding="utf-8"))
    except (OSError, ValueError) as exc:
        raise ConfigError(f"cannot read {path.name}: {type(exc).__name__}") from None


def load_catalog(path: Path = CATALOG_PATH, schemas: Path = SCHEMAS) -> EvalCatalog:
    """Load the catalog plus the contract enums it is read together with. Raises ConfigError."""
    raw = _read(path)
    result_schema = _read(schemas / "eval_result.v1.json")
    run_schema = _read(schemas / "agent_run.v1.json")
    try:
        entries = {name: CatalogEntry(eval_type=name, **body) for name, body in raw["eval_types"].items()}
        return EvalCatalog(
            version=raw["catalog_version"],
            evidence_classes=MappingProxyType(dict(raw["evidence_classes"])),
            entries=MappingProxyType(entries),
            diagnostics=tuple(result_schema["$defs"]["diagnostic"]["enum"]),
            workflows=tuple(run_schema["properties"]["workflow"]["enum"]),
        )
    except (KeyError, TypeError, ValidationError) as exc:
        raise ConfigError(f"eval catalog {path.name} is malformed: {type(exc).__name__}") from None
