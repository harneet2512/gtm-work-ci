"""The agent's tools. Every data tool is one read of core's POST /internal/ask/tools/{tool}; `propose_action`
only names a demo action for a human to confirm. There is deliberately no tool that sends or writes anything."""
from __future__ import annotations

from collections.abc import Mapping
from typing import Any, get_args

from .models import ActionKind

PROPOSE_TOOL = "propose_action"
ACTION_KINDS: tuple[str, ...] = get_args(ActionKind)
MAX_ARG_CHARS = 300

_ACCOUNT = "An account id or a name (for example MedTech)."
_RUN = "A run id, an episode id, or omit both and name the account to use its latest run."

# name -> (description, {argument: (type, description)}, required arguments)
_DATA_TOOLS: dict[str, tuple[str, dict[str, tuple[str, str]], tuple[str, ...]]] = {
    "list_accounts": ("List the accounts with their headline state.", {}, ()),
    "account_state": ("The account's state (stage, risks, people, commitments). as_of is a date or time in world time.",
                      {"account": ("string", _ACCOUNT), "as_of": ("string", "Optional date or RFC3339 time.")},
                      ("account",)),
    "timeline": ("The account's activities, newest first. from and to bound the dates.",
                 {"account": ("string", _ACCOUNT), "from": ("string", "Optional start date."),
                  "to": ("string", "Optional end date."), "limit": ("integer", "Optional, 1 to 50.")}, ("account",)),
    "graph_neighborhood": ("The account's graph neighborhood (stakeholders, claims, signals, decisions, knowledge).",
                           {"account": ("string", _ACCOUNT), "as_of": ("string", "Optional date or time.")},
                           ("account",)),
    "episode": ("A decision episode: the event that triggered it, the recommended and the chosen action, the outcome.",
                {"episode_id": ("string", "An episode id."), "account": ("string", _ACCOUNT + " Gives its latest episode.")},
                ()),
    "gate_results": ("The eval gate results (B1-B9, D1-D10, S1-S5) of an episode with verdict, why and evidence.",
                     {"episode_id": ("string", "An episode id."), "account": ("string", _ACCOUNT + " Gives its latest episode."),
                      "gate": ("string", "Optional gate such as D4.")}, ()),
    "strategies": ("The strategy options of a run, with their evals.",
                   {"run_id": ("string", _RUN), "episode_id": ("string", "An episode id."), "account": ("string", _ACCOUNT)}, ()),
    "human_decision": ("What the human chose and sent for a run.",
                       {"run_id": ("string", _RUN), "episode_id": ("string", "An episode id."), "account": ("string", _ACCOUNT)}, ()),
    "judgment_inference": ("How Cliff interpreted the human judgment of an episode.",
                           {"run_id": ("string", _RUN), "episode_id": ("string", "An episode id."), "account": ("string", _ACCOUNT)}, ()),
    "knowledge": ("The organization's learned knowledge, optionally for one account or status.",
                  {"account": ("string", _ACCOUNT + " Optional."), "status": ("string", "Optional lifecycle status.")}, ()),
    "knowledge_attribution": ("Which knowledge was retrieved, which applied and which was used by a run. Retrieval and use "
                              "are different from influence; never call it influence.",
                              {"run_id": ("string", _RUN), "episode_id": ("string", "An episode id."), "account": ("string", _ACCOUNT)}, ()),
    "search_activities": ("Search the text of activities (emails, calls, notes).",
                          {"query": ("string", "Words to look for."), "account": ("string", _ACCOUNT + " Optional.")}, ("query",)),
    "draft_followup": ("DRY RUN: gather what a follow-up should rest on (the account's latest strategy and people). "
                       "Nothing is sent. Label what you write DRAFT.",
                       {"account": ("string", _ACCOUNT), "intent": ("string", "What the follow-up should achieve.")},
                       ("account", "intent")),
    "crm_update_preview": ("DRY RUN: show what would change in the CRM for a field. Nothing is written.",
                           {"account": ("string", _ACCOUNT), "field": ("string", "The state field."),
                            "value": ("string", "The proposed value.")}, ("account", "field", "value")),
}
DATA_TOOLS: tuple[str, ...] = tuple(_DATA_TOOLS)


def _spec(name: str, description: str, props: dict[str, dict[str, Any]], required: tuple[str, ...]) -> dict[str, Any]:
    return {"type": "function", "function": {"name": name, "description": description, "parameters": {
        "type": "object", "additionalProperties": False, "properties": props, "required": list(required)}}}


TOOL_SPECS: list[dict[str, Any]] = [
    *[_spec(name, desc, {k: {"type": t, "description": dd} for k, (t, dd) in args.items()}, req)
      for name, (desc, args, req) in _DATA_TOOLS.items()],
    _spec(PROPOSE_TOOL,
          "Offer a state-changing demo action. Nothing runs: a human must confirm it first. Use play_next for 'play the "
          "next event', demo_status for the replay status. There is no reset.",
          {"kind": {"type": "string", "enum": list(ACTION_KINDS), "description": "The action."},
           "summary": {"type": "string", "description": "What it will do, in one sentence."}}, ("kind", "summary")),
]

ASK_SCHEMA_NAME = "ask_cliff_answer_v1"
ASK_SCHEMA: dict[str, Any] = {
    "type": "object", "additionalProperties": False,
    "required": ["answer_markdown", "cited_calls", "dont_know", "proposed_action"],
    "properties": {
        "answer_markdown": {"type": "string"},
        "cited_calls": {"type": "array", "items": {"type": "string"},
                        "description": "Ids (t1, t2, ...) of the tool calls whose data the answer rests on."},
        "dont_know": {"type": "boolean"},
        "proposed_action": {"type": ["object", "null"], "additionalProperties": False,
                            "required": ["kind", "summary"],
                            "properties": {"kind": {"type": "string", "enum": list(ACTION_KINDS)},
                                           "summary": {"type": "string"}}},
    },
}


def check_arguments(tool: str, arguments: Mapping[str, Any]) -> dict[str, Any]:
    """Validate model-supplied arguments of a data tool; raises ValueError with text safe to show the model."""
    if tool not in _DATA_TOOLS:
        raise ValueError(f"unknown tool {tool!r}")
    _, declared, required = _DATA_TOOLS[tool]
    unknown = set(arguments) - set(declared)
    if unknown:
        raise ValueError(f"unsupported arguments: {', '.join(sorted(unknown))}")
    missing = [r for r in required if not arguments.get(r)]
    if missing:
        raise ValueError(f"missing arguments: {', '.join(missing)}")
    clean: dict[str, Any] = {}
    for key, value in arguments.items():
        kind = declared[key][0]
        if kind == "integer":
            if isinstance(value, bool) or not isinstance(value, int) or not 1 <= value <= 50:
                raise ValueError(f"{key} must be an integer from 1 to 50")
        elif not isinstance(value, str) or len(value) > MAX_ARG_CHARS:
            raise ValueError(f"{key} must be text of at most {MAX_ARG_CHARS} characters")
        clean[key] = value
    return clean
