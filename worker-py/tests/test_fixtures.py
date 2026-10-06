"""WP1 (HAR-99) seed-world validation: events conform to the contracts and the normalization table,
the puzzles are present, and every gold checkpoint is schema-valid and grounded in verbatim evidence."""
from __future__ import annotations

import datetime as dt
import json
import re
from collections import defaultdict
from pathlib import Path

import pytest
from jsonschema import Draft202012Validator, FormatChecker
from referencing import Resource

from test_contracts import REGISTRY, SCHEMAS, load_json, validator

FIXTURES = Path(__file__).resolve().parents[2] / "fixtures"
ACCOUNTS = {"acme": "acme.com", "beta": "beta.io", "northstar": "northstar.health"}
OUR_DOMAIN = "ghostvendor.com"
WINDOW = (dt.datetime(2026, 8, 18, tzinfo=dt.timezone.utc), dt.datetime(2026, 9, 30, tzinfo=dt.timezone.utc))
SOURCE_KIND = {"email": "email", "calendar": "calendar_event", "call": "call", "crm": "crm_change",
               "slack": "slack_message", "enrichment": "enrichment", "docs": "document", "ghost.clock": "clock_tick"}
ACTIVITY_TYPES = set(load_json(SCHEMAS / "common.v1.json")["$defs"]["activityType"]["enum"])
STATE_FIELDS = set(load_json(SCHEMAS / "account_state.v1.json")["properties"]["fields"]["required"])
PERSON_FIELDS = ("owner", "champion", "economic_buyer")


def ts(value: str) -> dt.datetime:
    return dt.datetime.fromisoformat(value.replace("Z", "+00:00"))


def rel(path: Path) -> str:
    return path.relative_to(FIXTURES).as_posix()


WORLD_FILES = sorted(FIXTURES.glob("world/accounts/*/events/*.json"))
LIVE_FILES = sorted(FIXTURES.glob("live/*.json"))
EVENT_FILES = WORLD_FILES + LIVE_FILES
EVENTS = {rel(p): load_json(p) for p in EVENT_FILES}
GOLD_FILES = sorted(FIXTURES.glob("gold/*/cp*.json"))
GOLD_SCHEMA = load_json(FIXTURES / "gold" / "gold.schema.json")
GOLDS = [load_json(p) for p in GOLD_FILES]


def account_of(path: str) -> str:
    if path.startswith("world/accounts/"):
        return path.split("/")[2]
    return path.split("/")[1].split("_")[0]


# ---------- normalization table (contracts/normalization.md) ----------
def normalize(ev: dict) -> tuple[str, str, str]:
    """Return (source_object_id, activity_type, occurred_at) per the mapping table; assert key consistency."""
    system, key, p = ev["source_system"], ev["source_event_key"], ev["payload"]
    assert p["kind"] == SOURCE_KIND[system], "payload.kind does not match source_system"
    if system == "email":
        assert key == ("sent" if p["direction"] == "outbound" else "received")
        kind = "EmailSent" if p["direction"] == "outbound" else ("EmailReply" if p.get("in_reply_to") else "EmailReceived")
        return p["message_id"], kind, p["date"]
    if system == "calendar":
        return p["event_id"], *_calendar(ev, key, p)
    if system == "call":
        if p.get("transcript"):
            assert key == "transcript_ready"
            return p["call_id"], "TranscriptReady", p["ended_at"]
        assert key == "ended" and p.get("ended_at")
        return p["call_id"], "CallEnded", p["ended_at"]
    if system == "crm":
        return p["record_id"], _crm(key, p), p["changed_at"]
    if system == "slack":
        assert key == "posted"
        occurred = dt.datetime.fromtimestamp(int(p["ts"].split(".")[0]), dt.timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")
        return f"{p['channel']}:{p['ts']}", "SlackDecision" if p.get("is_decision") else "SlackMessage", occurred
    if system == "enrichment":
        assert key == "observed"
        subject = p["subject"].get("email") or p["subject"].get("domain")
        return f"{p['provider']}:{subject}:{p['observed_at']}", "EnrichmentUpdated", p["observed_at"]
    if system == "docs":
        assert key == f"shared:{p['shared_at']}"
        return p["document_id"], "DocumentShared", p["shared_at"]
    assert system == "ghost.clock" and key == "tick" and p["rule"] == "customer_silence"
    return f"{p['account_ref']}:{p['rule']}:{p['observed_at'][:10]}", "CustomerWentSilent", p["observed_at"]


def _calendar(ev: dict, key: str, p: dict) -> tuple[str, str]:
    responses = {a["email"]: a["response"] for a in p["attendees"]}
    if key == "completed":
        assert p["status"] == "completed"
        return "MeetingCompleted", p["end"]
    assert p["status"] == "scheduled" and ev.get("occurred_at"), "change events need occurred_at"
    if key == "scheduled":
        return "MeetingScheduled", ev["occurred_at"]
    if key.startswith("response:"):
        _, email, response = key.split(":")
        assert responses.get(email) == response and response in ("accepted", "declined")
        return ("MeetingAccepted" if response == "accepted" else "MeetingDeclined"), ev["occurred_at"]
    assert key.startswith("attendee_added:") and key.split(":", 1)[1] in responses
    return "MeetingParticipantAdded", ev["occurred_at"]


def _crm(key: str, p: dict) -> str:
    fields = p.get("fields") or {}
    if p["object_type"] == "Note":
        assert key == "created" and p.get("note_body")
        return "CRMNoteAdded"
    if p["object_type"] == "Contact" and p.get("created"):
        assert key == "created"
        return "ContactAdded"
    if p["object_type"] == "Opportunity" and "StageName" in fields:
        assert key == f"field:StageName:{fields['StageName']['new']}"
        return "OpportunityStageChanged"
    if p["object_type"] == "Account" and p.get("created"):
        assert key == "created"  # documented WP1 decision: the table has no Account-create row
        return "CRMFieldChanged"
    assert len(fields) == 1, "a non-create CRM change must touch exactly one field"
    (name, change), = fields.items()
    assert key == f"field:{name}:{change['new']}"
    return "StakeholderRoleChanged" if p["object_type"] == "Contact" and name in ("Role__c", "Title") else "CRMFieldChanged"


def body_text(ev: dict) -> str | None:
    p = ev["payload"]
    if p["kind"] == "email":
        return p["body_text"]
    if p["kind"] == "call" and p.get("transcript"):
        return "\n".join(f"{s['speaker']}: {s['text']}" for s in p["transcript"])
    if p["kind"] == "slack_message":
        return p["text"]
    if p["kind"] == "crm_change" and p["object_type"] == "Note":
        return p.get("note_body")
    return None


def triple(ev: dict) -> tuple[str, str, str]:
    return ev["source_system"], ev["source_object_id"], ev["source_event_key"]


# ---------- events ----------
@pytest.mark.parametrize("path", list(EVENTS), ids=list(EVENTS))
def test_event_conforms_to_contracts(path: str) -> None:
    ev = EVENTS[path]
    for name, doc in (("source_event", ev), ("source_payloads", ev["payload"])):
        errors = list(validator(name).iter_errors(doc))
        assert not errors, [f"{name} {list(e.absolute_path)}: {e.message}" for e in errors]


@pytest.mark.parametrize("path", list(EVENTS), ids=list(EVENTS))
def test_event_follows_normalization_table(path: str) -> None:
    ev = EVENTS[path]
    object_id, activity_type, occurred = normalize(ev)
    assert ev["source_object_id"] == object_id
    assert activity_type in ACTIVITY_TYPES
    assert ts(ev["occurred_at"]) == ts(occurred)
    assert WINDOW[0] <= ts(occurred) < WINDOW[1]


@pytest.mark.parametrize("path", list(EVENTS), ids=list(EVENTS))
def test_event_stays_inside_its_account_and_has_no_quoted_history(path: str) -> None:
    ev, acct = EVENTS[path], account_of(path)
    raw = json.dumps(ev["payload"])
    domains = set(re.findall(r"[\w.+-]+@([\w.-]+\.[a-z]+)", raw))
    allowed = (OUR_DOMAIN, ACCOUNTS[acct])
    assert all(d in allowed or d.endswith(tuple("." + a for a in allowed)) for d in domains), domains
    if ev["source_system"] == "slack":
        assert ev["payload"]["channel"] == f"#deal-{acct}"
    if ev["source_system"] == "email":
        body = ev["payload"]["body_text"]
        assert not re.search(r"^>|^On .+ wrote:|-----Original Message-----", body, re.M), "quoted reply history"
    if ev["source_system"] == "call" and ev["payload"].get("transcript"):
        labels = {s["label"] for s in ev["payload"]["speakers"]}
        assert {s["speaker"] for s in ev["payload"]["transcript"]} <= labels


@pytest.mark.parametrize("acct", ACCOUNTS)
def test_world_size_per_account(acct: str) -> None:
    n = len([p for p in WORLD_FILES if p.parent.parent.name == acct])
    assert 35 <= n <= 45, f"{acct}: {n} events"


def test_duplicate_deliveries_are_byte_identical() -> None:
    by_triple: dict[tuple, list[Path]] = defaultdict(list)
    for p in EVENT_FILES:
        by_triple[triple(EVENTS[rel(p)])].append(p)
    dups = {k: v for k, v in by_triple.items() if len(v) > 1}
    assert dups, "puzzle 2 needs at least one duplicate delivery"
    for paths in dups.values():
        assert len({p.read_bytes() for p in paths}) == 1
        assert all(p.name.endswith("_dup.json") for p in paths[1:]) and not paths[0].name.endswith("_dup.json")


def test_out_of_order_ingest_exists() -> None:
    def inverted(files: list[Path]) -> bool:
        times = [ts(EVENTS[rel(p)]["occurred_at"]) for p in files]
        return any(EVENTS[rel(files[i])]["source_event_key"] == "transcript_ready" and times[i] < max(times[:i])
                   and EVENTS[rel(files[i - 1])]["source_system"] == "email" for i in range(1, len(files)))
    assert any(inverted([p for p in WORLD_FILES if p.parent.parent.name == a]) for a in ACCOUNTS)


def test_unlabelled_speaker_puzzle_exists() -> None:
    unlabelled = [s for ev in EVENTS.values() if ev["source_system"] == "call"
                  for s in ev["payload"]["speakers"] if not s.get("email") and not s.get("name")]
    assert unlabelled, "puzzle 1 needs a speaker with no email and no name"


def test_org_contains_only_employees() -> None:
    org = load_json(FIXTURES / "world" / "org.json")
    assert org["organization"]["domain"] == OUR_DOMAIN
    assert org["people"] and all(p["kind"] == "employee" and p["email"].endswith("@" + OUR_DOMAIN)
                                 for p in org["people"])


def test_live_events_are_not_in_world() -> None:
    world_triples = {triple(EVENTS[rel(p)]) for p in WORLD_FILES}
    assert {rel(p) for p in LIVE_FILES} == {"live/acme_mnda_countersigned.json", "live/acme_marco_soc2_email.json",
                                            "live/beta_nonmaterial_event.json"}
    assert sorted(f for a in ACCOUNTS for f in live_order(a)) == sorted(rel(p) for p in LIVE_FILES)
    for p in LIVE_FILES:
        assert triple(EVENTS[rel(p)]) not in world_triples


SECRET_PATTERNS = [r"sk-[A-Za-z0-9_-]{16,}", r"AKIA[0-9A-Z]{16}", r"xox[abprs]-[A-Za-z0-9-]{10,}", r"gh[pousr]_[A-Za-z0-9]{20,}",
                   r"-----BEGIN [A-Z ]*PRIVATE KEY-----", r"(?i)(api[_-]?key|password|secret|token)\"?\s*[:=]\s*\"[^\"]{6,}",
                   r"eyJ[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}\."]


@pytest.mark.parametrize("path", sorted(FIXTURES.rglob("*.json")), ids=lambda p: rel(p))
def test_no_real_looking_secrets(path: Path) -> None:
    text = path.read_text(encoding="utf-8")
    for pattern in SECRET_PATTERNS:
        assert not re.search(pattern, text), pattern


# ---------- gold ----------
def gold_validator() -> Draft202012Validator:
    Draft202012Validator.check_schema(GOLD_SCHEMA)
    registry = REGISTRY.with_resource(GOLD_SCHEMA["$id"], Resource.from_contents(GOLD_SCHEMA))
    return Draft202012Validator(GOLD_SCHEMA, registry=registry, format_checker=FormatChecker())


def live_order(acct: str) -> list[str]:
    """Live files of an account in ingest order (gold `live_files`). Several live checkpoints may exist; each later one
    extends the previous one's list, so the longest list is the account's live stream."""
    orders = [g["live_files"] for g in sorted(GOLDS, key=lambda g: g["checkpoint"]) if g["account"] == acct and "live_files" in g]
    for earlier, later in zip(orders, orders[1:]):
        assert later[: len(earlier)] == earlier, f"{acct}: live_files of a later checkpoint must extend the earlier ones"
    return orders[-1] if orders else []


def ingested(acct: str, after: str) -> list[str]:
    """Event files ingested up to and including `after`: the world in file order, then the account's live files in order."""
    stream = [rel(p) for p in WORLD_FILES if p.parent.parent.name == acct] + live_order(acct)
    return stream[: stream.index(after) + 1]


def included(gold: dict) -> list[str]:
    return ingested(gold["account"], gold["after_event_file"])


def test_twelve_gold_checkpoints() -> None:
    assert sorted(rel(p) for p in GOLD_FILES) == sorted(f"gold/{a}/cp{i}.json" for a in ACCOUNTS for i in range(1, 5))


def test_gold_schema_state_fields_match_account_state() -> None:
    names = set(GOLD_SCHEMA["properties"]["expected"]["properties"]["state_fields"]["propertyNames"]["enum"])
    assert names == STATE_FIELDS


@pytest.mark.parametrize("path", GOLD_FILES, ids=lambda p: rel(p))
def test_gold_validates_and_is_grounded(path: Path) -> None:
    gold = load_json(path)
    errors = list(gold_validator().iter_errors(gold))
    assert not errors, [f"{list(e.absolute_path)}: {e.message}" for e in errors]
    assert path.parent.name == gold["account"] and path.stem == f"cp{gold['checkpoint']}"
    assert gold["after_event_file"] in EVENTS
    assert gold.get("live_files", [gold["after_event_file"]])[-1] == gold["after_event_file"]
    files = included(gold)
    exp = gold["expected"]
    assert gold["as_of"] == max((EVENTS[f]["occurred_at"] for f in files), key=ts)
    assert exp.get("distinct_activities", len({triple(EVENTS[f]) for f in files})) == len({triple(EVENTS[f]) for f in files})
    keys = {e["key"] for e in exp["entities"]}
    assert all(e["src"] in keys and e["dst"] in keys for e in exp["edges"])
    assert all(b["person_key"] in keys and b.get("delegated_to", b["person_key"]) in keys for b in exp["buying_group"])
    for field in PERSON_FIELDS:
        value = exp["state_fields"][field]
        assert value == "unknown" or value in keys, (field, value)
    for fact in exp["critical_facts"]:
        assert fact["evidence_event_file"] in files, f"evidence {fact['evidence_event_file']} not ingested yet"
        ev = EVENTS[fact["evidence_event_file"]]
        if fact["evidence_quote"] is None:
            assert ev["source_system"] == "crm" and body_text(ev) is None, "only structured CRM evidence may omit a quote"
        else:
            assert fact["evidence_quote"] in (body_text(ev) or ""), f"not verbatim: {fact['evidence_quote']!r}"
    for stale in exp["stale_facts"]:
        assert stale["source_event_file"] in files


@pytest.mark.parametrize("acct", ACCOUNTS)
def test_material_diff_fields_match_consecutive_states(acct: str) -> None:
    prev = {"state_fields": {}, "buying_group": [], "coverage_gaps": []}
    for i in range(1, 5):
        exp = load_json(FIXTURES / "gold" / acct / f"cp{i}.json")["expected"]
        changed = {f for f in STATE_FIELDS if prev["state_fields"].get(f, "unknown") != exp["state_fields"].get(f, "unknown")}
        changed |= {k for k in ("buying_group", "coverage_gaps") if prev[k] != exp[k]}
        assert set(exp["material_diff_fields"]) <= changed, (acct, i)
        prev = exp


def test_acme_checkpoints_split_world_and_live() -> None:
    acme_world = [rel(p) for p in WORLD_FILES if p.parent.parent.name == "acme"]
    assert load_json(FIXTURES / "gold/acme/cp3.json")["after_event_file"] == acme_world[-1]
    cp4 = load_json(FIXTURES / "gold/acme/cp4.json")
    assert cp4["after_event_file"] == "live/acme_marco_soc2_email.json" and cp4["expected"]["trigger"]["eligible"]
    # Acme cp4 = world + the countersigned NDA (09-28) + Marco's email: the SOC2 package is no longer gated on the NDA.
    assert cp4["live_files"] == ["live/acme_mnda_countersigned.json", "live/acme_marco_soc2_email.json"]
    assert included(cp4) == acme_world + cp4["live_files"], "a live checkpoint = whole world, then live_files in order"
    beta4 = load_json(FIXTURES / "gold/beta/cp4.json")["expected"]
    assert beta4["material_diff_fields"] == [] and not beta4["trigger"]["eligible"]


def test_gold_conflicts_follow_the_standing_order() -> None:
    """ADR-0008/0009: a surfaced conflict's winner outranks the contradicting claim in the five-value standing order."""
    order = load_json(SCHEMAS / "common.v1.json")["$defs"]["standing"]["enum"]
    assert order == ["human_approved", "crm_explicit", "first_party_record", "first_party_ai", "third_party"]
    conflicts = [c for g in GOLDS for c in g["expected"].get("conflicts", [])]
    assert conflicts, "Beta cp3/cp4 surface the next_milestone conflict"
    for c in conflicts:
        assert order.index(c["winner_standing"]) < order.index(c["contradicting_standing"]), c["field"]


@pytest.mark.parametrize(("mutate", "why"), [
    (lambda g: g.pop("live_files"), "a live checkpoint must list its live files"),
    (lambda g: g.update(after_event_file="world/accounts/acme/events/044_email_dana_security_review_ask.json"),
     "a world checkpoint must not list live files"),
    (lambda g: g.update(live_files=["world/accounts/acme/events/044_email_dana_security_review_ask.json"]),
     "live_files hold live/ files only"),
], ids=["missing", "world_with_live_files", "non_live_item"])
def test_gold_live_files_rule_rejects(mutate, why: str) -> None:
    gold = json.loads((FIXTURES / "gold/acme/cp4.json").read_text(encoding="utf-8"))
    mutate(gold)
    assert list(gold_validator().iter_errors(gold)), why
