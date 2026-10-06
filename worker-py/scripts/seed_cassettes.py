"""Compute cassette keys for the hand-written responses in tests/fixtures and write cassettes/.

Run after any change to the extract prompt/schema (which changes cassette keys):
    cd worker-py && python scripts/seed_cassettes.py
The parent replaces these with recorded cassettes later (GHOST_LLM_MODE=record).
"""
from __future__ import annotations

import json
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(ROOT))

from ghost_worker.extract.prompt import build_system_prompt, build_user_prompt  # noqa: E402
from ghost_worker.extract.schema import OUTPUT_SCHEMA, OUTPUT_SCHEMA_NAME  # noqa: E402
from ghost_worker.llm.fake_provider import cassette_key, model_family, schema_sha256, write_cassette  # noqa: E402
from ghost_worker.models import ExtractRequest  # noqa: E402
from ghost_worker.settings import Settings  # noqa: E402

FIXTURES = ROOT / "tests" / "fixtures"
PAIRS = [("extract_email_request.json", "email_response.json"),
         ("extract_transcript_request.json", "transcript_response.json"),
         ("extract_injection_request.json", "injection_response.json")]


def main() -> None:
    family = model_family(Settings(_env_file=None).ghost_model)
    fresh: set[str] = set()
    for request_name, response_name in PAIRS:
        request = ExtractRequest.model_validate(json.loads((FIXTURES / request_name).read_text(encoding="utf-8")))
        response = json.loads((FIXTURES / response_name).read_text(encoding="utf-8"))
        system, user = build_system_prompt(), build_user_prompt(request)
        key = cassette_key(family, system, user, OUTPUT_SCHEMA_NAME, OUTPUT_SCHEMA)
        document = {"key": key, "hand_written": True,
                    "request": {"model_family": family, "system": system, "user": user,
                                "schema_name": OUTPUT_SCHEMA_NAME,
                                "schema_sha256": schema_sha256(OUTPUT_SCHEMA)},
                    "response": response}
        path = write_cassette(ROOT / "cassettes", key, document)
        fresh.add(path.name)
        sys.stdout.write(f"{request_name} -> {path.name}\n")
    for stale in (ROOT / "cassettes").glob("*.json"):  # drop superseded HAND-WRITTEN cassettes only
        if stale.name not in fresh and json.loads(stale.read_text(encoding="utf-8")).get("hand_written"):
            stale.unlink()
            sys.stdout.write(f"removed stale {stale.name}\n")


if __name__ == "__main__":
    main()
