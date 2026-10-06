"""WP32 (HAR-131) realism pass: the paraphrase validator, the cassette modes, resumability and byte-identical replay.

No model is called: the provider is a stub. Cassettes are written to tmp_path (real ones stay in git-ignored data/).
"""
from __future__ import annotations

import json
import sys
from pathlib import Path
from types import SimpleNamespace

import pytest

ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(ROOT / "bench"))

from ghost_worker.errors import DailyCapError  # noqa: E402
from synthetic import paraphrase, pipeline, rules  # noqa: E402
from synthetic.paraphrase_check import check  # noqa: E402

SAMPLE = ROOT / "fixtures" / "crmarena_sample"
SLOTS = {"seller_first": "Dana", "buyer_full": "Priya Shah", "company": "Acme Corp", "product": "Smart Hub", "date": "March 4"}
TEMPLATE = "Hi Dana,\n\nWe can move on Smart Hub by March 4.\n\nThanks,\nPriya Shah\nAcme Corp"


def test_a_faithful_paraphrase_is_accepted() -> None:
    text = "Hi Dana,\n\nWe are able to move on Smart Hub by March 4.\n\nBest regards,\nPriya Shah\nAcme Corp"
    assert check(text, TEMPLATE, SLOTS) == []


@pytest.mark.parametrize("text,needle", [
    ("Hi Dana,\n\nWe can move on the hub by March 4.\n\nPriya Shah\nAcme Corp", "product"),   # slot dropped
    ("Hi Dan,\n\nWe can move on Smart Hub by March 4.\n\nPriya Shah\nAcme Corp", "seller_first"),  # slot altered
    ("Hi Dana,\n\nWe can move on Smart Hub by March 4 for 25 seats.\n\nPriya Shah\nAcme Corp", "added numbers"),
    ("Hi Dana,\n\nWe can move on Smart Hub by March 4, maybe twelve units.\n\nPriya Shah\nAcme Corp", "number words"),
    ("Hi Dana,\n\nWe can move on Smart Hub by March 4 or in June.\n\nPriya Shah\nAcme Corp", "dates"),
    ("Hi Dana,\n\nWe can move on Smart Hub by March 4.\n\nPriya Shah\nAcme Corp priya@acme.com", "email"),
    ("Hi Dana,\n\nWe can move on Smart Hub by March 4. See www.acme.com.\n\nPriya Shah\nAcme Corp", "link"),
    ("Hi Dana,\n\nMarcus and I can move on Smart Hub by March 4.\n\nPriya Shah\nAcme Corp", "names"),
    ("   ", "empty"),
])
def test_an_unfaithful_paraphrase_is_rejected(text: str, needle: str) -> None:
    assert any(needle in r for r in check(text, TEMPLATE, SLOTS)), check(text, TEMPLATE, SLOTS)


def test_vocabulary_of_real_emails_allows_ordinary_capitalised_words() -> None:
    text = "Hi Dana,\n\nHonestly we can move on Smart Hub by March 4.\n\nPriya Shah\nAcme Corp"
    assert any("names" in r for r in check(text, TEMPLATE, SLOTS))
    assert check(text, TEMPLATE, SLOTS, vocab={"honestly"}) == []


def test_contractions_and_modal_may_are_not_names_or_dates() -> None:
    text = "Hi Dana,\n\nI’ll check; I'm sure we may move on Smart Hub by March 4.\n\nPriya Shah\nAcme Corp"
    assert check(text, TEMPLATE, SLOTS) == []


def test_rejudge_writes_a_new_set_and_never_touches_the_original(tmp_path: Path) -> None:
    src, out = tmp_path / "src", tmp_path / "rejudged"
    _collect(src, 1)
    tail = "Original email to rewrite:\n"
    paraphrase.run_pending(src, ["x"], set(), Stub(reply=lambda u: u.split(tail, 1)[1] + "\nHonestly."))
    before = paraphrase.manifest(src)["cassettes_sha256"]
    assert paraphrase.status(src)["rejected"] == 1
    res = paraphrase.rejudge(src, {"honestly"}, out)
    assert res["changed"] == 1 and res["before"]["rejected"] == 1 and res["after"]["rejected"] == 0
    assert paraphrase.manifest(src)["cassettes_sha256"] == before  # the original set is untouched
    assert res["cassettes_sha256"] == paraphrase.manifest(out)["cassettes_sha256"] != before
    with pytest.raises(ValueError):
        paraphrase.rejudge(src, set(), out)  # never into an existing set
    with pytest.raises(ValueError):
        paraphrase.rejudge(src, set(), src)


def test_cassette_key_depends_on_template_slots_and_seed() -> None:
    k = paraphrase.cassette_key("t#1", SLOTS, "1:x:p1")
    assert k == paraphrase.cassette_key("t#1", dict(SLOTS), "1:x:p1")
    assert len({k, paraphrase.cassette_key("t#2", SLOTS, "1:x:p1"), paraphrase.cassette_key("t#1", {**SLOTS, "company": "B"}, "1:x:p1"),
                paraphrase.cassette_key("t#1", SLOTS, "2:x:p1")}) == 4


class Stub:
    """A provider stub: answers with a faithful rewrite, counts calls, optionally raises the daily cap after n."""

    def __init__(self, reply=None, cap_after: int | None = None) -> None:
        self.calls, self.reply, self.cap_after = 0, reply, cap_after

    def complete_json(self, *, system, user, schema, schema_name):
        if self.cap_after is not None and self.calls >= self.cap_after:
            raise DailyCapError("cap", resume_in_s=3600)
        self.calls += 1
        body = self.reply(user) if self.reply else user.split("Original email to rewrite:\n", 1)[1].replace("Hi ", "Hello ")
        return SimpleNamespace(content={"body": body}, model="stub/free")


def _collect(directory: Path, texts: int = 3) -> paraphrase.Paraphraser:
    p = paraphrase.Paraphraser("collect", directory, 7, "style")
    for i in range(texts):
        slots = {**SLOTS, "company": f"Acme {chr(65 + i)}"}
        p.text("messages.x#1", slots, TEMPLATE.replace("Acme Corp", slots["company"]))
    p.flush_requests()
    return p


def test_modes_off_collect_replay(tmp_path: Path) -> None:
    assert paraphrase.Paraphraser("off", tmp_path, 7).text("t#1", SLOTS, TEMPLATE) == TEMPLATE
    _collect(tmp_path)
    assert len((tmp_path / "requests.jsonl").read_text().splitlines()) == 3
    slots = {**SLOTS, "company": "Acme A"}
    strict = paraphrase.Paraphraser("replay", tmp_path, 7, "style")
    with pytest.raises(paraphrase.ParaphraseError, match="no cassette"):  # replay never silently falls back
        strict.text("messages.x#1", slots, TEMPLATE)
    lax = paraphrase.Paraphraser("replay", tmp_path, 7, "style", allow_fallback=True)
    assert lax.text("messages.x#1", slots, TEMPLATE) == TEMPLATE and lax.stats["missing"] == 1
    with pytest.raises(ValueError):
        paraphrase.Paraphraser("bogus", tmp_path, 7)


def test_run_pending_is_resumable_and_stops_on_the_daily_cap(tmp_path: Path) -> None:
    _collect(tmp_path)
    first = Stub(cap_after=2)
    paraphrase.run_pending(tmp_path, ["An example email body."], set(), first)
    assert first.calls == 2 and paraphrase.status(tmp_path)["remaining"] == 1
    second = Stub()
    paraphrase.run_pending(tmp_path, ["An example email body."], set(), second)
    assert second.calls == 1 and paraphrase.status(tmp_path)["remaining"] == 0
    third = Stub()
    paraphrase.run_pending(tmp_path, ["An example email body."], set(), third)
    assert third.calls == 0


def test_replay_fails_on_a_rejected_cassette_unless_fallback_is_allowed(tmp_path: Path) -> None:
    _collect(tmp_path, 2)
    paraphrase.run_pending(tmp_path, ["An example email body."], set(), Stub(reply=lambda u: "Hello there, 25 seats are fine."))
    st = paraphrase.status(tmp_path)
    assert st["rejected"] == 2 and st["rejection_rate"] == 1.0 and st["accepted"] == 0
    slots, text = {**SLOTS, "company": "Acme A"}, TEMPLATE.replace("Acme Corp", "Acme A")
    with pytest.raises(paraphrase.ParaphraseError, match="fails validation"):
        paraphrase.Paraphraser("replay", tmp_path, 7, "style").text("messages.x#1", slots, text)
    lax = paraphrase.Paraphraser("replay", tmp_path, 7, "style", allow_fallback=True)
    assert lax.text("messages.x#1", slots, text) == text and lax.stats["rejected_fallback"] == 1


def test_replay_revalidates_and_does_not_trust_the_stored_status(tmp_path: Path) -> None:
    _collect(tmp_path, 1)
    paraphrase.run_pending(tmp_path, ["x"], set(), Stub())
    (req,) = paraphrase._requests(tmp_path)
    cassette = paraphrase.load_cassette(tmp_path, req["key"])
    assert cassette["status"] == "accepted"
    tampered = {**cassette, "text": cassette["text"].replace("Smart Hub", "the hub")}  # status still says accepted
    paraphrase._save(tmp_path, tampered)
    with pytest.raises(paraphrase.ParaphraseError):
        paraphrase.Paraphraser("replay", tmp_path, 7, "style").text(req["template_id"], req["slots"], req["template_text"])


def test_manifest_hash_changes_with_a_cassette(tmp_path: Path) -> None:
    _collect(tmp_path, 1)
    before = paraphrase.manifest(tmp_path)["cassettes_sha256"]
    paraphrase.run_pending(tmp_path, ["An example email body."], set(), Stub())
    assert paraphrase.manifest(tmp_path)["cassettes_sha256"] != before


def _files(out: Path) -> dict[str, bytes]:
    return {p.relative_to(out).as_posix(): p.read_bytes() for p in sorted(out.rglob("*")) if p.is_file()}


def test_replay_generation_is_byte_identical_and_changes_only_email_bodies(tmp_path: Path) -> None:
    rs, doc = rules.load_rules(), rules.load_doc()
    pdir = tmp_path / "para"
    base_out = tmp_path / "off"
    pipeline.generate_to(SAMPLE, base_out, rs, doc)
    pipeline.generate_to(SAMPLE, tmp_path / "collect", rs, doc, ("collect", pdir))
    assert paraphrase.status(pdir)["requests"] > 0
    paraphrase.run_pending(pdir, ["An example real email."], set(), Stub(reply=lambda u: u.split("Original email to rewrite:\n", 1)[1] + "\n(rewritten)"))
    with pytest.raises(paraphrase.ParaphraseError):  # no cassettes at all: replay never falls back silently
        pipeline.generate_to(SAMPLE, tmp_path / "strict", rs, doc, ("replay", tmp_path / "empty"))
    a = pipeline.generate_to(SAMPLE, tmp_path / "a", rs, doc, ("replay", pdir))
    b = pipeline.generate_to(SAMPLE, tmp_path / "b", rs, doc, ("replay", pdir))
    assert _files(tmp_path / "a") == _files(tmp_path / "b") and a["manifest_sha256"] == b["manifest_sha256"]
    assert a["paraphrase"]["stats"].get("paraphrased", 0) > 0 and not a["paraphrase"]["stats"].get("missing")
    assert a["paraphrase"]["cassettes_sha256"] == b["paraphrase"]["cassettes_sha256"]
    assert a["paraphrase"]["cassettes_sha256"] == paraphrase.manifest(pdir)["cassettes_sha256"]  # every cassette was used
    off, on = _files(base_out), _files(tmp_path / "a")
    assert set(off) == set(on)
    changed = [n for n in off if off[n] != on[n] and n.startswith("events/")]
    assert changed
    for name in changed:
        for e_off, e_on in zip(json.loads(off[name]), json.loads(on[name])):
            p_off, p_on = dict(e_off["payload"]), dict(e_on["payload"])
            p_on.pop("body_text", None)
            p_off.pop("body_text", None)
            assert p_off == p_on and e_off["source_object_id"] == e_on["source_object_id"]
            assert e_on["origin"] == "synthetic" and e_on["provenance"] == "synthetic:v1"
