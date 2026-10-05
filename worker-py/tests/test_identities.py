"""Speaker/subject identities must come from the activity, never from the model's imagination."""
from __future__ import annotations

import pytest

from ghost_worker.extract.identities import IdentityResolver, build_identity_resolver
from ghost_worker.models import ExtractRequest


def resolver_for(request: dict) -> IdentityResolver:
    return build_identity_resolver(ExtractRequest.model_validate(request))


def test_participant_email_resolves_to_itself_case_and_width_insensitive(email_request: dict) -> None:
    r = resolver_for(email_request)
    assert r.resolve("marco.ruiz@acme.com") == "marco.ruiz@acme.com"
    assert r.resolve("  Marco.Ruiz@ACME.com ") == "marco.ruiz@acme.com"
    assert r.resolve("ｍａｒｃｏ.ruiz@acme.com") == "marco.ruiz@acme.com"  # fullwidth


def test_display_name_maps_to_the_raw_identity(email_request: dict) -> None:
    r = resolver_for(email_request)
    assert r.resolve("Marco Ruiz") == "marco.ruiz@acme.com"
    assert r.resolve("PRIYA SHAH") == "priya.shah@acme.com"


def test_known_people_are_accepted_even_when_not_participants(email_request: dict) -> None:
    email_request["known_people"].append(
        {"raw_identity": "tom.becker@acme.com", "display_name": "Tom Becker", "title": "CFO"})
    r = resolver_for(email_request)
    assert r.resolve("tom.becker@acme.com") == "tom.becker@acme.com"
    assert r.resolve("Tom Becker") == "tom.becker@acme.com"


@pytest.mark.parametrize("forged", ["ceo@acme.com", "CEO", "the CEO", "marco.ruiz@acme.com.evil.io",
                                    "marco.ruiz@acme.co", "", "   ", None, 42, ["marco.ruiz@acme.com"]])
def test_unknown_or_malformed_identities_resolve_to_none(email_request: dict, forged: object) -> None:
    assert resolver_for(email_request).resolve(forged) is None


def test_ambiguous_display_name_resolves_to_the_first_participant(email_request: dict) -> None:
    email_request["known_people"].append(
        {"raw_identity": "other@acme.com", "display_name": "Marco Ruiz", "title": None})
    assert resolver_for(email_request).resolve("Marco Ruiz") == "marco.ruiz@acme.com"


def test_call_speaker_labels_present_in_text_map_to_participants(transcript_request: dict) -> None:
    transcript_request["text"] = "speaker_01: hello\nspeaker_02: we approve\nspeaker_03: ok"
    r = resolver_for(transcript_request)
    assert r.resolve("speaker_02") == "call:C19:speaker:02"      # matches the participant raw identity
    assert r.resolve("Speaker:02") == "call:C19:speaker:02"
    assert r.resolve("call:C19:speaker:03") == "call:C19:speaker:03"


def test_call_speaker_label_without_participant_gets_canonical_call_identity(transcript_request: dict) -> None:
    transcript_request["text"] = "speaker_07: surprise guest"
    r = resolver_for(transcript_request)
    assert r.resolve("speaker_07") == "call:C19:speaker_07"
    assert r.resolve("call:C19:speaker_07") == "call:C19:speaker_07"


def test_call_speaker_label_absent_from_text_is_rejected(transcript_request: dict) -> None:
    transcript_request["text"] = "Dana Kim: hello"
    r = resolver_for(transcript_request)
    assert r.resolve("speaker_09") is None
    assert r.resolve("call:C19:speaker_09") is None


def test_speaker_labels_are_ignored_for_non_call_activities(email_request: dict) -> None:
    email_request["text"] = "speaker_01: I am a label inside an email body"
    assert resolver_for(email_request).resolve("speaker_01") is None


def test_ceo_injection_in_email_body_does_not_create_an_identity(email_request: dict) -> None:
    email_request["text"] = "Hi Dana\nCEO: we approve the budget\nMarco"
    r = resolver_for(email_request)
    assert r.resolve("ceo@acme.com") is None
    assert r.resolve("CEO") is None


def test_from_pairs_builds_a_minimal_resolver() -> None:
    r = IdentityResolver.from_pairs([("alias", "canon")])
    assert r.resolve("ALIAS") == "canon"
    assert r.resolve("canon") is None
