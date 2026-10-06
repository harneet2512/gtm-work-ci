"""What a judge may see: only the evidence-backed context of one candidate action.

AccountState (subset), recent changes, trigger and supporting activities, commitments, timeline facts, offered
knowledge and the candidate AgentRunOutput. A rep profile is carried separately and shown only to judges whose
rubric asks for it (rep style, HAR-97 §24), so style never reaches the strategy judges. Gold labels, rationales,
case titles, notes and slice tags of a benchmark case are never part of it.
"""
from __future__ import annotations

from collections.abc import Mapping
from types import MappingProxyType
from typing import Annotated, Any

from pydantic import AfterValidator, BaseModel, ConfigDict, PlainSerializer, ValidationError

from ..errors import InvalidRequestError
from ..models.draft import AgentRunOutput

DEFAULT_WORKFLOW = "post_interaction_followup"
# Keys of an offline eval case's `context` a judge may see (fixtures/evals/case.schema.json).
CASE_CONTEXT_KEYS = ("now", "state", "recent_changes", "trigger", "supporting_activities", "commitments",
                     "timeline_facts", "offered_knowledge")
# Fixture bookkeeping on case activities (which file, invented or not): not evidence, never shown to a judge.
FIXTURE_ONLY_KEYS = frozenset({"event_file", "synthetic"})
STRUCTURAL_REFS = frozenset({"buying_group", "coverage_gaps", "conflicts"})


def deep_freeze(value: Any) -> Any:
    """dict -> read-only mapping, list -> tuple, recursively (immutable values)."""
    if isinstance(value, Mapping):
        return MappingProxyType({k: deep_freeze(v) for k, v in value.items()})
    if isinstance(value, (list, tuple)):
        return tuple(deep_freeze(v) for v in value)
    return value


def thaw(value: Any) -> Any:
    """The JSON-ready (plain dict/list) copy of a frozen value."""
    if isinstance(value, Mapping):
        return {k: thaw(v) for k, v in value.items()}
    if isinstance(value, tuple):
        return [thaw(v) for v in value]
    return value


def _evidence_only(activity: Mapping[str, Any]) -> dict[str, Any]:
    return {k: v for k, v in activity.items() if k not in FIXTURE_ONLY_KEYS}


Frozen = Annotated[Any, AfterValidator(deep_freeze), PlainSerializer(thaw)]


class JudgeContext(BaseModel):
    model_config = ConfigDict(frozen=True, extra="forbid")

    now: str
    state: Frozen
    recent_changes: Frozen
    trigger: Frozen
    supporting_activities: Frozen = ()
    commitments: Frozen = ()
    timeline_facts: Frozen = MappingProxyType({})
    offered_knowledge: Frozen = ()
    candidate_action: Frozen
    rep_profile: Frozen = None
    workflow: str = DEFAULT_WORKFLOW

    @classmethod
    def build(cls, context: Mapping[str, Any], candidate_action: Mapping[str, Any], *,
              rep_profile: Mapping[str, Any] | None = None, workflow: str = DEFAULT_WORKFLOW) -> JudgeContext:
        """Validate the candidate as an AgentRunOutput and freeze everything. Raises InvalidRequestError."""
        try:
            AgentRunOutput.model_validate(dict(candidate_action))
            return cls(**{k: context[k] for k in CASE_CONTEXT_KEYS if k in context},
                       candidate_action=candidate_action, rep_profile=rep_profile, workflow=workflow)
        except (ValidationError, KeyError, TypeError) as exc:
            raise InvalidRequestError(f"judge context is invalid: {type(exc).__name__}") from None

    @classmethod
    def from_eval_case(cls, case: Mapping[str, Any]) -> JudgeContext:
        """Only the case's context and candidate action; never `expected`, `expected_best_action`, title, notes,
        slice tags, case type or difficulty."""
        context = dict(case["context"])
        context["trigger"] = _evidence_only(context["trigger"])
        context["supporting_activities"] = [_evidence_only(a) for a in context.get("supporting_activities", [])]
        return cls.build(context, case["candidate_action"], rep_profile=context.get("rep_profile"))

    # ---------- views ----------
    @property
    def action_type(self) -> str:
        return self.candidate_action["proposed_action_type"]

    @property
    def fields(self) -> Mapping[str, Any]:
        return self.state.get("fields", MappingProxyType({}))

    def activities(self) -> Mapping[str, Mapping[str, Any]]:
        """Activities the judge may cite, by activity id: the trigger and the supporting activities."""
        return MappingProxyType({a["activity_id"]: a for a in (self.trigger, *self.supporting_activities)})

    def candidate_cited(self) -> frozenset[str]:
        """Ids of activities the candidate cites that the judge is not given as text. A judge never saw them, so
        they are unverifiable: they may be echoed without error but are never evidence."""
        known = self.activities()
        return frozenset(r["activity_id"] for r in self.candidate_action.get("evidence_refs", ())
                         if r["activity_id"] not in known)

    def knowledge_ids(self) -> frozenset[str]:
        return frozenset(k["id"] for k in self.offered_knowledge)

    def state_ref_names(self) -> frozenset[str]:
        """Names a judgment may cite in state_refs: the state's fields plus its structural parts."""
        return frozenset(self.fields) | (STRUCTURAL_REFS & frozenset(self.state))

    def payload(self, *, include_rep_profile: bool) -> dict[str, Any]:
        """JSON-ready context for a judge prompt; the rep profile only when the rubric uses it."""
        body = {
            "now": self.now, "workflow": self.workflow, "account_state": thaw(self.state),
            "recent_changes": thaw(self.recent_changes), "trigger": thaw(self.trigger),
            "supporting_activities": thaw(self.supporting_activities), "commitments": thaw(self.commitments),
            "timeline_facts": thaw(self.timeline_facts), "offered_knowledge": thaw(self.offered_knowledge),
            "candidate_action": thaw(self.candidate_action),
        }
        if include_rep_profile:
            body["rep_profile"] = thaw(self.rep_profile)
        return body
