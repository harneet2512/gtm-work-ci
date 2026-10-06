"""The CRMArena extraction prompt still hashes to the key the demo cassettes were recorded under.

All recorded extraction cassettes (data/crmarena_extraction/cassettes) keyed the prompt with the seller domain
`ghostvendor.com`. The brand rename once changed that domain, which changed the "known people" block of every prompt
and so every exact cassette key. The fixture holds the structured request of one recorded activity and the key the
recording has; the test rebuilds the prompt with the worker's own builder and the replay worker's key function."""
from __future__ import annotations

import json
import sys
from pathlib import Path

from ghost_worker.extract.prompt import build_system_prompt, build_user_prompt
from ghost_worker.extract.service import OUTPUT_SCHEMA, OUTPUT_SCHEMA_NAME
from ghost_worker.llm.fake_provider import schema_sha256
from ghost_worker.models import ExtractRequest

ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(ROOT / "bench" / "data"))
from crmarena_replay_worker import replay_key  # noqa: E402

FIXTURE = json.loads((Path(__file__).parent / "fixtures" / "crmarena_recorded_prompt.json").read_text(encoding="utf-8"))
SELLER_DOMAIN = "ghostvendor.com"


def key_of(request: dict) -> str:
    prompt = build_user_prompt(ExtractRequest.model_validate(request))
    return replay_key(build_system_prompt(), prompt, OUTPUT_SCHEMA_NAME, schema_sha256(OUTPUT_SCHEMA))


def test_recorded_activity_prompt_hashes_to_the_recorded_cassette_key() -> None:
    assert key_of(FIXTURE["request"]) == FIXTURE["recorded_replay_key"]


def test_seller_side_identities_use_the_recorded_domain() -> None:
    request = FIXTURE["request"]
    sellers = [p["raw_identity"] for p in request["known_people"] if p["internal"]]
    assert sellers and all(s.endswith("@" + SELLER_DOMAIN) for s in sellers)
    assert all(p["raw_identity"].endswith("@" + SELLER_DOMAIN) for p in request["activity"]["participants"] if p["role"] == "from")


def test_a_different_seller_domain_misses_the_recording() -> None:
    renamed = json.loads(json.dumps(FIXTURE["request"]).replace(SELLER_DOMAIN, "vendor.example"))
    assert key_of(renamed) != FIXTURE["recorded_replay_key"]
