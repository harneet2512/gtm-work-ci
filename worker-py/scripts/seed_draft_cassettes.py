"""Write the hand-written account-agent cassettes (cassettes/draft/) from tests/draft_situations.py.

Run after any change to the draft prompts, tool specs or schemas (which changes cassette keys):
    cd worker-py && python scripts/seed_draft_cassettes.py
Each situation runs once through RecordingProvider(ScriptedProvider) against a fake core (httpx.MockTransport):
no network, no model. The scripted responses are the hand-authored expectations, marked "hand_written": true.
"""
from __future__ import annotations

import json
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(ROOT))
sys.path.insert(0, str(ROOT / "tests"))

import draft_doubles as dbl  # noqa: E402
import draft_situations as sit  # noqa: E402
from ghost_worker.draft.core_client import CoreContextClient  # noqa: E402
from ghost_worker.draft.deadline import Deadline  # noqa: E402
from ghost_worker.draft.service import draft_action  # noqa: E402
from ghost_worker.llm.fake_provider import RecordingProvider, model_family, write_cassette  # noqa: E402
from ghost_worker.models.draft import DraftRequest  # noqa: E402
from ghost_worker.settings import Settings  # noqa: E402

OUT = ROOT / "cassettes" / "draft"


def _drop_stale_hand_written() -> None:
    for path in OUT.glob("*.json"):
        if json.loads(path.read_text(encoding="utf-8")).get("hand_written") is True:
            path.unlink()


def main() -> None:
    family = model_family(Settings(_env_file=None).ghost_model)
    OUT.mkdir(parents=True, exist_ok=True)
    _drop_stale_hand_written()
    for situation in sit.SITUATIONS.values():
        provider = RecordingProvider(dbl.ScriptedProvider(list(situation.turns), situation.skill), OUT, family)
        request = DraftRequest.model_validate(situation.request_copy())
        core = dbl.FakeCore(situation.packets)
        with CoreContextClient(dbl.CORE_URL, request.run_token, timeout_s=5.0, max_bytes=16384,
                               transport=core.transport) as client:
            response = draft_action(request, provider, client, deadline=Deadline(60))
        sys.stdout.write(f"{situation.name}: {response.output.proposed_action_type}\n")
    for path in OUT.glob("*.json"):  # everything just recorded came from scripted, hand-authored responses
        document = json.loads(path.read_text(encoding="utf-8"))
        write_cassette(OUT, document["key"], {**document, "hand_written": True})
    sys.stdout.write(f"{len(list(OUT.glob('*.json')))} cassettes in {OUT}\n")


if __name__ == "__main__":
    main()
