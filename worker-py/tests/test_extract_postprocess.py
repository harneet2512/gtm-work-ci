"""Post-processing is the trust boundary between model output and ClaimCandidates."""
from __future__ import annotations

import math

import pytest

from ghost_worker.errors import InvalidModelOutputError
from ghost_worker.extract.identities import IdentityResolver
from ghost_worker.extract.postprocess import MAX_CLAIMS, postprocess
from ghost_worker.models.claim_candidate import MAX_EVIDENCE_QUOTE_CHARS

TEXT = "Hi Dana - I lead security at Acme.\nWe'll need your  SOC2 Type II report.\nCafé \U0001F600 ok"


def cand(**overrides: object) -> dict:
    base = {"field_path": "blockers", "value": "SOC2 Type II report", "confidence": 0.9,
            "evidence_quote": "SOC2 Type II report", "speaker_identity": "marco@acme.com",
            "subject_identity": None, "role": None, "due_at": None}
    base.update(overrides)
    return base


RESOLVER = IdentityResolver.from_pairs([("marco@acme.com", "marco@acme.com"),
                                        ("marco.ruiz@acme.com", "marco.ruiz@acme.com")])


def run(*items: object, text: str = TEXT):
    return postprocess({"claims": list(items)}, text, RESOLVER)


def test_verbatim_candidate_is_kept_unchanged() -> None:
    result = run(cand())
    assert len(result.claims) == 1
    claim = result.claims[0]
    assert (claim.field_path, claim.value, claim.confidence) == ("blockers", "SOC2 Type II report", 0.9)
    assert claim.evidence_quote in TEXT
    assert result.dropped == 0


def test_whitespace_variant_quote_is_replaced_with_exact_substring() -> None:
    # model collapsed the double space and the newline
    result = run(cand(evidence_quote="at Acme. We'll need your SOC2 Type II report"))
    assert result.dropped == 0
    assert result.claims[0].evidence_quote == "at Acme.\nWe'll need your  SOC2 Type II report"
    assert result.claims[0].evidence_quote in TEXT


def test_quote_with_surrounding_whitespace_is_trimmed_to_exact_substring() -> None:
    result = run(cand(evidence_quote="  I lead security at Acme.  "))
    assert result.claims[0].evidence_quote == "I lead security at Acme."


def test_invented_quote_is_dropped_and_counted() -> None:
    result = run(cand(evidence_quote="We will definitely sign next week"), cand())
    assert len(result.claims) == 1
    assert result.dropped == 1


def test_quote_matching_is_case_sensitive() -> None:
    result = run(cand(evidence_quote="i lead security at acme."))
    assert result.claims == ()
    assert result.dropped == 1


def test_unicode_and_emoji_quotes_survive() -> None:
    result = run(cand(evidence_quote="Café \U0001F600 ok"))
    assert result.claims[0].evidence_quote == "Café \U0001F600 ok"


def test_repeated_normalized_quote_resolves_to_first_occurrence() -> None:
    text = "ok  go\nand ok-go"
    result = postprocess({"claims": [cand(evidence_quote="ok go")]}, text, RESOLVER)
    assert result.claims[0].evidence_quote == "ok  go"


@pytest.mark.parametrize("quote", ["", "   ", None, 7, ["x"]])
def test_unusable_quotes_are_dropped_and_counted(quote: object) -> None:
    result = run(cand(evidence_quote=quote))
    assert result.claims == ()
    assert result.dropped == 1


def test_quote_longer_than_contract_limit_is_dropped() -> None:
    assert MAX_EVIDENCE_QUOTE_CHARS == 2000
    long_text = "a" * (MAX_EVIDENCE_QUOTE_CHARS + 1)
    result = postprocess({"claims": [cand(evidence_quote=long_text)]}, long_text, RESOLVER)
    assert result.claims == ()
    assert result.dropped == 1


def test_field_path_outside_enum_is_discarded_but_not_counted_as_quote_drop() -> None:
    result = run(cand(field_path="mood"), cand(field_path=None), cand(field_path=3))
    assert result.claims == ()
    assert result.dropped == 0
    assert result.discarded == 3


@pytest.mark.parametrize(("given", "expected"), [(0, 0.0), (1, 1.0), (0.42, 0.42)])
def test_in_range_confidence_is_kept(given: float, expected: float) -> None:
    assert run(cand(confidence=given)).claims[0].confidence == expected


@pytest.mark.parametrize("given", [1.7, -3, -0.0001, 1.0001, math.inf, -math.inf])
def test_out_of_range_confidence_is_discarded_not_clamped(given: float) -> None:
    result = run(cand(confidence=given))
    assert result.claims == ()
    assert (result.discarded, result.dropped) == (1, 0)


@pytest.mark.parametrize("bad", ["0.9", None, True, [0.9], math.nan, "high"])
def test_non_numeric_confidence_is_discarded(bad: object) -> None:
    result = run(cand(confidence=bad))
    assert result.claims == ()
    assert result.discarded == 1


@pytest.mark.parametrize("value", [None, "", "   "])
def test_empty_values_are_discarded(value: object) -> None:
    assert run(cand(value=value)).discarded == 1


def test_missing_value_is_discarded() -> None:
    item = cand()
    del item["value"]
    assert run(item).discarded == 1


def test_unknown_is_a_legal_value() -> None:
    assert run(cand(field_path="next_meeting", value="unknown")).claims[0].value == "unknown"


def test_list_value_is_preserved() -> None:
    assert run(cand(value=["a", "b"])).claims[0].value == ["a", "b"]


def test_valid_role_is_kept_and_none_is_allowed() -> None:
    assert run(cand(role="security")).claims[0].role == "security"
    assert run(cand(role=None)).claims[0].role is None


@pytest.mark.parametrize("role", ["wizard", "", 5, ["security"]])
def test_invalid_role_discards_the_candidate(role: object) -> None:
    result = run(cand(role=role))
    assert result.claims == ()
    assert result.discarded == 1


def test_identities_resolve_to_canonical_and_blank_means_none() -> None:
    claim = run(cand(speaker_identity="  MARCO@acme.com ", subject_identity="   ")).claims[0]
    assert claim.speaker_identity == "marco@acme.com"
    assert claim.subject_identity is None


def test_unknown_identity_is_nulled_but_claim_is_kept() -> None:
    claim = run(cand(speaker_identity="ceo@acme.com", subject_identity="cfo@acme.com")).claims[0]
    assert (claim.speaker_identity, claim.subject_identity) == (None, None)


def test_non_string_identity_becomes_none() -> None:
    assert run(cand(speaker_identity=42)).claims[0].speaker_identity is None


def test_due_at_parsed_when_timezone_aware() -> None:
    claim = run(cand(field_path="commitment", due_at="2026-10-08T10:00:00Z")).claims[0]
    assert claim.due_at is not None and claim.due_at.isoformat() == "2026-10-08T10:00:00+00:00"


@pytest.mark.parametrize("bad", ["next Friday", "2026-10-08T10:00:00", "", 123])
def test_unparseable_or_naive_due_at_becomes_none(bad: object) -> None:
    claim = run(cand(due_at=bad)).claims[0]
    assert claim.due_at is None


def test_identical_candidates_are_deduplicated() -> None:
    result = run(cand(), cand(), cand(confidence=0.5))
    assert len(result.claims) == 2  # the third differs in confidence


def test_dedupe_ignores_quote_whitespace_variants_after_normalization() -> None:
    result = run(cand(evidence_quote="SOC2 Type II report"), cand(evidence_quote="SOC2   Type II report"))
    assert len(result.claims) == 1


def test_cap_keeps_highest_confidence_in_original_order() -> None:
    items = [cand(value=f"v{i}", confidence=0.5 + (i % 2) * 0.01) for i in range(40)]
    result = run(*items)
    assert len(result.claims) == MAX_CLAIMS == 25
    assert result.truncated == 15
    kept = [c.value for c in result.claims]
    assert kept == sorted(kept, key=lambda v: int(v[1:]))  # original order
    assert sum(c.confidence == 0.51 for c in result.claims) == 20  # every high-confidence item survives


def test_empty_claims_list_is_fine() -> None:
    result = run()
    assert (result.claims, result.dropped, result.discarded) == ((), 0, 0)


@pytest.mark.parametrize("raw", [None, [], "claims", 3, {}, {"claims": None}, {"claims": "x"}, {"claims": {"a": 1}}])
def test_structurally_malformed_output_raises(raw: object) -> None:
    with pytest.raises(InvalidModelOutputError):
        postprocess(raw, TEXT, RESOLVER)


def test_non_dict_items_are_discarded() -> None:
    result = run("string", 5, None, ["x"], cand())
    assert len(result.claims) == 1
    assert result.discarded == 4


def test_extra_keys_in_candidate_are_ignored() -> None:
    assert len(run(cand(reasoning="because", extra=1)).claims) == 1


def test_large_input_performance() -> None:
    big_text = ("The customer needs SOC2.  " * 2000)[:60000]
    items = [cand(value=f"v{i}", evidence_quote="The customer needs SOC2.") for i in range(500)]
    assert len(postprocess({"claims": items}, big_text, RESOLVER).claims) == MAX_CLAIMS


# --- verbatim matching edge cases (CRLF, NBSP, NFC/NFD, boundaries, blank lines) ---------------------

def quote_of(text: str, quote: str):
    result = postprocess({"claims": [cand(evidence_quote=quote)]}, text, RESOLVER)
    return result, (result.claims[0].evidence_quote if result.claims else None)


def test_crlf_text_matches_lf_quote_and_returns_original_slice() -> None:
    text = "Budget is approved.\r\nWe will sign in Q4.\r\nThanks"
    result, quote = quote_of(text, "approved.\nWe will sign in Q4.")
    assert quote == "approved.\r\nWe will sign in Q4."
    assert quote in text and result.dropped == 0


def test_crlf_exact_quote_is_kept() -> None:
    text = "one\r\ntwo"
    assert quote_of(text, "one\r\ntwo")[1] == "one\r\ntwo"


def test_nbsp_in_text_matches_plain_space_quote() -> None:
    text = "We need Q4\u00a0budget\u202fsign-off now"
    _, quote = quote_of(text, "need Q4 budget sign-off")
    assert quote == "need Q4\u00a0budget\u202fsign-off"
    assert quote in text


def test_nfd_text_matches_nfc_quote_and_returns_original_slice() -> None:
    text = "Meet at the Cafe\u0301 on Friday"          # NFD e + combining acute
    result, quote = quote_of(text, "the Caf\u00e9 on")  # NFC quote
    assert quote == "the Cafe\u0301 on"
    assert quote in text and result.dropped == 0


def test_nfc_text_matches_nfd_quote() -> None:
    text = "Meet at the Caf\u00e9 on Friday"
    _, quote = quote_of(text, "Cafe\u0301 on Friday")
    assert quote == "Caf\u00e9 on Friday"


def test_exact_substring_cutting_a_combining_mark_is_still_verbatim() -> None:
    text = "the Cafe\u0301 opens"
    assert quote_of(text, "the Cafe")[1] == "the Cafe"  # literally a substring of the text


def test_quote_at_very_start_and_very_end_of_text() -> None:
    text = "First words here and last words there"
    assert quote_of(text, "First words")[1] == "First words"
    assert quote_of(text, "last  words there")[1] == "last words there"  # whitespace-variant at the end
    assert quote_of(text, text)[1] == text


def test_whitespace_variant_at_start_and_end_maps_to_exact_offsets() -> None:
    text = "alpha  beta\ngamma delta"
    assert quote_of(text, "alpha beta gamma")[1] == "alpha  beta\ngamma"
    assert quote_of(text, "beta gamma delta")[1] == "beta\ngamma delta"


@pytest.mark.parametrize("sep", ["\n\n", "\r\n\r\n", "\n \n", "\n\t\n", "\n\n\n"])
def test_normalized_match_may_not_span_a_blank_line(sep: str) -> None:
    text = f"Para one ends here.{sep}Para two starts here."
    result, _ = quote_of(text, "ends here. Para two")
    assert result.claims == ()
    assert result.dropped == 1


def test_exact_quote_spanning_a_blank_line_is_still_verbatim() -> None:
    text = "Para one.\n\nPara two."
    assert quote_of(text, text)[1] == text


def test_later_occurrence_without_blank_line_is_used() -> None:
    text = "go now.\n\nnext step\n\nthen go now.\nnext step"
    # first normalized occurrence of "now. next step" spans a blank line; the last one does not
    assert quote_of(text, "then go now. next step")[1] == "then go now.\nnext step"
    result, _ = quote_of(text, "go now. next step")
    assert result.claims[0].evidence_quote == "go now.\nnext step"


def test_single_newline_with_indentation_is_not_a_blank_line() -> None:
    text = "line one\n    indented two"
    assert quote_of(text, "line one indented two")[1] == text


def test_quote_not_in_text_after_all_normalizations_is_dropped() -> None:
    result, _ = quote_of("Caf\u00e9 is open", "Cafe is open")
    assert result.claims == () and result.dropped == 1
