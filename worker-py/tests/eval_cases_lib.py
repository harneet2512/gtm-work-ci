"""WP16 (HAR-114) eval benchmark: loaders, validators (built once), catalog access and summary tables.

Run `python tests/eval_cases_lib.py` from worker-py to print every table used in docs/traceability/wp16.md."""
from __future__ import annotations

from collections import Counter, defaultdict

from jsonschema import Draft202012Validator, FormatChecker
from referencing import Resource

from test_contracts import CONTRACTS, REGISTRY, load_json, validator
from test_fixtures import FIXTURES, GOLD_SCHEMA

EVALS = FIXTURES / "evals"
CASE_SCHEMA = load_json(EVALS / "case.schema.json")
IDS = load_json(EVALS / "ids.json")
CASE_FILES = sorted(EVALS.glob("cases/*/*.json"))
CASES = {p.stem: load_json(p) for p in CASE_FILES}
# Source messages of the invented (synthetic) activities, one SourceEvent per activity id.
SYNTHETIC_EVENTS = {p.stem: load_json(p) for p in sorted(EVALS.glob("synthetic/*.json"))}
MAX_LINES = 400

# The one eval vocabulary (contracts/evals/eval_catalog.json): nothing below copies it.
CATALOG = load_json(CONTRACTS / "evals" / "eval_catalog.json")
EVAL_TYPES: dict[str, dict] = CATALOG["eval_types"]
CASE_TYPES = list(CATALOG["case_types"])

PEOPLE_BY_ID = {v["id"]: {**v, "key": k} for k, v in IDS["people"].items()}
ACTIVITY_FILE = {v: k for k, v in IDS["activities"].items()}
SYN_PREFIX = IDS["synthetic_activity_prefix"]
PERSON_FIELDS = ("owner", "champion", "economic_buyer")


def _case_validator() -> Draft202012Validator:
    Draft202012Validator.check_schema(CASE_SCHEMA)
    registry = REGISTRY.with_resources([(GOLD_SCHEMA["$id"], Resource.from_contents(GOLD_SCHEMA)),
                                        (CASE_SCHEMA["$id"], Resource.from_contents(CASE_SCHEMA))])
    return Draft202012Validator(CASE_SCHEMA, registry=registry, format_checker=FormatChecker())


CASE_VALIDATOR = _case_validator()
OUTPUT_VALIDATOR = validator("agent_run_output")
KNOWLEDGE_VALIDATOR = validator("knowledge")


def judgments() -> list[tuple[dict, dict]]:
    """(case, expected judgment) pairs over the whole benchmark."""
    return [(c, e) for c in CASES.values() for e in c["expected"]]


def activities(case: dict) -> list[dict]:
    return [case["context"]["trigger"], *case["context"]["supporting_activities"]]


def evidence_refs(node: object) -> list[dict]:
    """Every evidenceRef-shaped dict anywhere in the node (candidate action, buying group, ...)."""
    if isinstance(node, dict):
        found = [node] if "activity_id" in node and "text" not in node else []
        return found + [r for v in node.values() for r in evidence_refs(v)]
    if isinstance(node, list):
        return [r for v in node for r in evidence_refs(v)]
    return []


def person_ids(case: dict) -> set[str]:
    ctx, cand = case["context"], case["candidate_action"]
    ids = {r["person_id"] for r in cand["recipients"]}
    ids |= {r["person_id"] for r in case["expected_best_action"].get("recipients", [])}
    ids |= {m["person_id"] for m in ctx["state"]["buying_group"]}
    ids |= {m["delegated_to_person_id"] for m in ctx["state"]["buying_group"] if m["delegated_to_person_id"]}
    ids |= {c["owner_person_id"] for c in ctx["commitments"] if c["owner_person_id"]}
    ids |= {a["actor_person_id"] for a in activities(case) if a["actor_person_id"]}
    ids |= {r["speaker_person_id"] for r in evidence_refs(case) if "speaker_person_id" in r}
    for name, value in ctx["state"]["fields"].items():
        if name in PERSON_FIELDS and value != "unknown":
            ids.add(value)
        if isinstance(value, list):
            ids |= {i["owner_person_id"] for i in value if "owner_person_id" in i}
    return ids


def to_gold(value: object) -> object:
    """Case state value -> gold checkpoint representation (person UUIDs back to entity keys)."""
    if isinstance(value, str) and value in PEOPLE_BY_ID:
        return PEOPLE_BY_ID[value]["key"]
    if isinstance(value, list):
        return [{("owner" if k == "owner_person_id" else k): (PEOPLE_BY_ID[v]["key"] if k == "owner_person_id" else v)
                 for k, v in item.items()} for item in value]
    return value


# ---------- tables ----------
def _group_key(case: dict, by: str) -> str:
    if by in ("case_type", "difficulty"):
        return case[by]
    return str(case["slice_tags"][by])


def summary_table(by: str = "case_type") -> list[tuple]:
    """Rows (group, cases, should_pass, flawed, blocking_fails, wait_or_no_action) grouped by case_type, difficulty
    or a slice tag."""
    rows: dict[str, Counter] = defaultdict(Counter)
    for c in CASES.values():
        row = rows[_group_key(c, by)]
        row["cases"] += 1
        row["should_pass"] += c["should_pass"]
        row["flawed"] += not c["should_pass"]
        row["blocking_fails"] += sum(e["blocking"] for e in c["expected"])
        row["wait_or_no_action"] += c["expected_best_action"]["action"] in ("wait", "no_action")
    cols = ("cases", "should_pass", "flawed", "blocking_fails", "wait_or_no_action")
    return [(k, *(rows[k][col] for col in cols)) for k in sorted(rows)]


def format_table(by: str = "case_type") -> str:
    head = f"{by:<32} cases pass flawed blocking wait/none"
    return "\n".join([head] + [f"{r[0]:<32} {r[1]:>5} {r[2]:>4} {r[3]:>6} {r[4]:>8} {r[5]:>9}" for r in summary_table(by)])


def eval_type_table() -> list[tuple]:
    """Rows (eval_type, kind, evidence_class, judgments, pass, warn, fail, abstain, blocking, debatable) per catalog type."""
    rows: dict[str, Counter] = defaultdict(Counter)
    for _, e in judgments():
        row = rows[e["eval_type"]]
        row["judgments"] += 1
        row[e["verdict"]] += 1
        row["blocking"] += e["blocking"]
        row["debatable"] += e.get("debatable", False)
    cols = ("judgments", "pass", "warn", "fail", "abstain", "blocking", "debatable")
    return [(t, EVAL_TYPES[t]["kind"], EVAL_TYPES[t]["evidence_class"], *(rows[t][col] for col in cols)) for t in EVAL_TYPES]


def format_eval_type_table() -> str:
    head = f"{'eval_type':<28} {'kind':<13} {'class':<12} n pass warn fail abst block debat"
    return "\n".join([head] + [f"{r[0]:<28} {r[1]:<13} {r[2]:<12} {r[3]:>2} {r[4]:>4} {r[5]:>4} {r[6]:>4} {r[7]:>4} {r[8]:>5} "
                               f"{r[9]:>5}" for r in eval_type_table()])


if __name__ == "__main__":
    for by in ("case_type", "difficulty", "account", "origin", "motion", "risk", "champion_status", "stage", "candidate"):
        print(format_table(by), end="\n\n")
    print(format_eval_type_table())
