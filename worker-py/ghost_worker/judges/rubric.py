"""Rubric files (judges/rubrics/<eval_type>.json): the per-eval judging criteria, as data.

A rubric adds what the catalog does not hold: the HAR-97 questions, inputs, criteria (Check / Evaluate / Combine),
failure examples, label guides, diagnostic guides, and
routing. `load_rubrics` checks every rubric against the catalog so neither can drift from the other.
"""
from __future__ import annotations

import json
from collections.abc import Mapping
from pathlib import Path
from types import MappingProxyType
from typing import Annotated, Literal

from pydantic import BaseModel, ConfigDict, Field, StringConstraints, ValidationError

from ..errors import ConfigError
from ..models.draft import ProposedAction
from .catalog import CatalogEntry, EvalCatalog
from .predicates import PREDICATES

RUBRIC_DIR = Path(__file__).resolve().parent / "rubrics"
Snake = Annotated[str, StringConstraints(pattern=r"^[a-z][a-z0-9_]*$")]
Text = Annotated[str, StringConstraints(min_length=1, max_length=600)]


class _Frozen(BaseModel):
    model_config = ConfigDict(frozen=True, extra="forbid")


class Har97Ref(_Frozen):
    """A line of the HAR-97 description (numbered as in docs/traceability/wp18_har97_lines.txt), quoted exactly."""

    line: Annotated[int, Field(ge=1)]
    text: Text


class Question(_Frozen):
    section: Text
    text: Text


class Criterion(_Frozen):
    id: Snake
    kind: Literal["check", "evaluate", "combine"]
    text: Text
    guide: Text
    har97: Har97Ref | None = None
    output: Literal["finding", "direction_magnitude"] = "finding"
    before: Text | None = None
    after: Text | None = None


class LabelGuide(_Frozen):
    label: Annotated[str, StringConstraints(pattern=r"^[A-Z][A-Z_]*$")]
    when: Text


class DiagnosticGuide(_Frozen):
    name: Snake
    when: Text


class Routing(_Frozen):
    """Selected when the proposed action is listed, every `when_all` predicate holds and, if `when_any` is not
    empty, at least one of its predicates holds (HAR-97 §7)."""

    actions: Annotated[tuple[ProposedAction, ...], Field(min_length=1)]
    when_all: tuple[Snake, ...] = ()
    when_any: tuple[Snake, ...] = ()


class Rubric(_Frozen):
    eval_type: Snake
    version: Annotated[int, Field(ge=1)]
    title: Text
    har97_refs: tuple[Har97Ref, ...]
    l2_dimension: Text | None = None
    questions: Annotated[tuple[Question, ...], Field(min_length=1)]
    inputs: tuple[Text, ...]
    criteria: Annotated[tuple[Criterion, ...], Field(min_length=1)]
    failure_examples: tuple[Text, ...] = ()
    counter_failures: tuple[Text, ...] = ()
    known_exceptions: tuple[Text, ...] = ()
    candidate_choices: tuple[Text, ...] = ()
    principles: tuple[Text, ...] = ()
    labels: tuple[LabelGuide, ...] = ()
    pass_label: str | None = None
    diagnostics: tuple[DiagnosticGuide, ...] = ()
    routing: Routing
    uses_rep_profile: bool = False

    @property
    def eval_version(self) -> str:
        """'<eval_type>:v<N>' (HAR-97 §21)."""
        return f"{self.eval_type}:v{self.version}"

    @property
    def schema_name(self) -> str:
        return f"semantic_judge_{self.eval_type}_v{self.version}"

    def criterion_ids(self) -> tuple[str, ...]:
        return tuple(c.id for c in self.criteria)


def _check_labels(rubric: Rubric, entry: CatalogEntry) -> None:
    guided = [g.label for g in rubric.labels]
    if sorted(guided) != sorted(entry.labels) or len(set(guided)) != len(guided):
        raise ConfigError(f"rubric {rubric.eval_type}: label guides {guided} != catalog labels {list(entry.labels)}")
    if entry.labels and rubric.pass_label not in entry.labels:
        raise ConfigError(f"rubric {rubric.eval_type}: pass_label must be one of the catalog labels")
    if not entry.labels and rubric.pass_label is not None:
        raise ConfigError(f"rubric {rubric.eval_type}: an unlabelled eval has no pass_label")


def _check_blocking(rubric: Rubric, entry: CatalogEntry) -> None:
    """Blocking is the catalog's: every semantic type must carry a blocking_rule there (rubrics hold none)."""
    if not entry.blocking_rule:
        raise ConfigError(f"rubric {rubric.eval_type}: the catalog has no blocking_rule for it; blocking is "
                          "defined only in contracts/evals/eval_catalog.json")


def _check_vocabulary(rubric: Rubric, catalog: EvalCatalog) -> None:
    for d in rubric.diagnostics:
        if d.name not in catalog.diagnostics:
            raise ConfigError(f"rubric {rubric.eval_type}: diagnostic {d.name} is not in the EvalResult contract")
    unknown = set(rubric.routing.when_all + rubric.routing.when_any) - set(PREDICATES)
    if unknown:
        raise ConfigError(f"rubric {rubric.eval_type}: unknown routing predicates {sorted(unknown)}")
    ids = rubric.criterion_ids()
    if len(set(ids)) != len(ids):
        raise ConfigError(f"rubric {rubric.eval_type}: duplicate criterion ids")
    for c in rubric.criteria:
        if (c.output == "direction_magnitude") != (c.before is not None and c.after is not None):
            raise ConfigError(f"rubric {rubric.eval_type}: criterion {c.id} needs before/after examples exactly "
                              "when it reports direction and magnitude")


def check_rubric(rubric: Rubric, catalog: EvalCatalog) -> Rubric:
    entry = catalog.entry(rubric.eval_type)
    if entry.kind != "semantic":
        raise ConfigError(f"rubric {rubric.eval_type}: only semantic evals have judge rubrics")
    _check_labels(rubric, entry)
    _check_blocking(rubric, entry)
    _check_vocabulary(rubric, catalog)
    return rubric


def load_rubric(path: Path, catalog: EvalCatalog) -> Rubric:
    try:
        rubric = Rubric.model_validate(json.loads(path.read_text(encoding="utf-8")))
    except (OSError, ValueError, ValidationError) as exc:
        raise ConfigError(f"rubric {path.name} is unreadable or malformed: {type(exc).__name__}") from None
    if path.stem != rubric.eval_type:
        raise ConfigError(f"rubric {path.name} declares eval_type {rubric.eval_type}")
    return check_rubric(rubric, catalog)


def load_rubrics(catalog: EvalCatalog, directory: Path = RUBRIC_DIR) -> Mapping[str, Rubric]:
    """One rubric per semantic catalog type, keyed and ordered as the catalog. Raises ConfigError."""
    rubrics = {p.stem: load_rubric(p, catalog) for p in sorted(directory.glob("*.json"))}
    missing = set(catalog.semantic_types()) - set(rubrics)
    if missing:
        raise ConfigError(f"no rubric for semantic eval types {sorted(missing)}")
    return MappingProxyType({name: rubrics[name] for name in catalog.order(set(rubrics))})
