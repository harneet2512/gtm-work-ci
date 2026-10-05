"""Write the hand-written principle-judge cassettes (cassettes/knowledge/) from tests/knowledge_lessons.py.

Run after any change to the judge prompt or schema (which changes cassette keys):
    cd worker-py && python scripts/seed_knowledge_cassettes.py
No network and no model: the responses are the hand-authored labels of the spec-derived cases, marked
"hand_written": true. Replace them with GHOST_LLM_MODE=record runs before reporting judge agreement.
"""
from __future__ import annotations

import json
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(ROOT))
sys.path.insert(0, str(ROOT / "tests"))

import knowledge_lessons as kl  # noqa: E402
from ghost_worker.knowledge_evals.principle import SCHEMA, SCHEMA_NAME, SYSTEM, build_user_prompt  # noqa: E402
from ghost_worker.llm.fake_provider import cassette_key, model_family, schema_sha256, write_cassette  # noqa: E402
from ghost_worker.settings import Settings  # noqa: E402

OUT = ROOT / "cassettes" / "knowledge"


def main() -> None:
    family = model_family(Settings(_env_file=None).ghost_model)
    for stale in OUT.glob("*.json"):
        if json.loads(stale.read_text(encoding="utf-8")).get("hand_written") is True:
            stale.unlink()
    for case in kl.CASES:
        if case.judge_label is None or case.inferred is None:
            continue
        user = build_user_prompt(case.correction, case.reference, case.inferred)
        key = cassette_key(family, SYSTEM, user, SCHEMA_NAME, SCHEMA)
        document = {
            "key": key, "hand_written": True,
            "request": {"model_family": family, "system": SYSTEM, "user": user, "schema_name": SCHEMA_NAME,
                        "schema_sha256": schema_sha256(SCHEMA)},
            "response": {"content": {"label": case.judge_label, "rationale": kl.JUDGE_RATIONALES[case.judge_label]},
                         "model": f"hand_written/{family}", "usage": {}},
        }
        path = write_cassette(OUT, key, document)
        sys.stdout.write(f"{case.id} -> {path.name}\n")


if __name__ == "__main__":
    main()
