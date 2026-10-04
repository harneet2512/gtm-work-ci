"""Offline worker for the CRMArena extraction cassettes: replays a re-import without any network or key (HAR-104 / HAR-130).

    python bench/data/crmarena_replay_worker.py --cassettes data/crmarena_extraction/cassettes [--port 8090]

The stock replay mode keys a cassette on the exact prompt. A fresh import changes two things in it: the delimiter nonce, which is derived
from the activity UUID (worker-py extract/prompt.py choose_nonce), and, for a few accounts, the known-people block (ties ordered
differently, one shared address classified on the other side). This worker therefore answers in two tiers: (1) exact, with the nonce
masked; (2) same email, ignoring the known-people block, logged as "approximate replay" and counted. A missing cassette is an error,
never a live call: this process has no provider and reads no API key.
"""
from __future__ import annotations

import argparse
import hashlib
import json
import logging
import re
import sys
from pathlib import Path
from typing import Any

ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(ROOT / "worker-py"))

log = logging.getLogger("ghost_worker.replay")
_NONCE = re.compile(r"TEXT-[0-9a-f]{16}")
_MARK = "TEXT-NONCE\n"


def mask_nonce(user_prompt: str) -> str:
    return _NONCE.sub("TEXT-NONCE", user_prompt)


def _digest(parts: list[str]) -> str:
    return hashlib.sha256(json.dumps(parts, ensure_ascii=False).encode("utf-8")).hexdigest()


def replay_key(system: str, user: str, schema_name: str, schema_sha256: str) -> str:
    """Exact tier: the whole prompt with the nonce masked."""
    return _digest([system, mask_nonce(user), schema_name, schema_sha256])


def loose_key(system: str, user: str, schema_name: str, schema_sha256: str) -> str:
    """Approximate tier: the activity header line and the email text only (not the participants or known-people blocks)."""
    masked = mask_nonce(user)
    header = masked.split("\n", 1)[0]
    text = masked.split(_MARK, 1)[1] if _MARK in masked else ""
    return _digest([system, header, text, schema_name, schema_sha256])


class MaskedReplayProvider:
    """LLMProvider that answers from recorded cassettes; it cannot call a model."""

    def __init__(self, directory: Path, dump_misses: Path | None = None) -> None:
        self.dump_misses = dump_misses
        self.index: dict[str, dict[str, Any]] = {}
        self.loose: dict[str, dict[str, Any]] = {}
        self.exact_hits = self.approximate_hits = 0
        # One entry per approximate hit, in call order: the activity header line and a short digest of the loose key.
        # The client attributes each hit to its event; the log lets a reader audit which recordings were reused.
        self.approximate_log: list[dict[str, str]] = []
        for path in sorted(directory.glob("*.json")):
            doc = json.loads(path.read_text(encoding="utf-8"))
            req = doc["request"]
            if "user" not in req:  # tool-calling turns are not extraction cassettes
                continue
            args = (req["system"], req["user"], req["schema_name"], req["schema_sha256"])
            self.index[replay_key(*args)] = doc["response"]
            self.loose.setdefault(loose_key(*args), doc["response"])

    def complete_json(self, *, system: str, user: str, schema: dict[str, Any], schema_name: str) -> Any:
        from ghost_worker.errors import CassetteNotFoundError
        from ghost_worker.llm.fake_provider import schema_sha256
        from ghost_worker.llm.provider import LLMResult

        digest = schema_sha256(schema)
        key = replay_key(system, user, schema_name, digest)
        if key in self.index:
            self.exact_hits += 1
            return LLMResult.model_validate(self.index[key])
        loose = loose_key(system, user, schema_name, digest)
        if loose in self.loose:
            self.approximate_hits += 1
            self.approximate_log.append({"activity": mask_nonce(user).split("\n", 1)[0], "loose_key": loose[:12]})
            log.warning("approximate replay: the known-people block differs from the recording")
            return LLMResult.model_validate(self.loose[loose])
        if self.dump_misses is not None:  # debugging aid: contains email text, keep it under the git-ignored data/
            self.dump_misses.mkdir(parents=True, exist_ok=True)
            (self.dump_misses / f"{key}.txt").write_text(user, encoding="utf-8")
        raise CassetteNotFoundError(f"no cassette for prompt {key[:12]} (replay never calls a model)")

    def complete_turn(self, **_: Any) -> Any:
        raise NotImplementedError("the extraction replay worker serves /v1/extract only")


def replay_stats(provider: MaskedReplayProvider) -> dict[str, Any]:
    """The counters served at GET /replay-stats (kept small: callers poll it once per extraction)."""
    return {"cassettes": len(provider.index), "exact_hits": provider.exact_hits, "approximate_hits": provider.approximate_hits}


def main() -> None:
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("--cassettes", type=Path, required=True)
    ap.add_argument("--port", type=int, default=8090)
    ap.add_argument("--dump-misses", type=Path, help="write the prompt of every cassette miss here (debugging)")
    args = ap.parse_args()
    import uvicorn
    from ghost_worker.app import create_app
    from ghost_worker.settings import Settings

    provider = MaskedReplayProvider(args.cassettes, args.dump_misses)
    print(f"{len(provider.index)} cassettes indexed", flush=True)
    app = create_app(settings=Settings(ghost_llm_mode="replay", openrouter_api_key=None, max_concurrency=16), provider=provider)
    # Counters for callers that need to report how the replay was served (demo-case mining): GET /replay-stats.
    app.add_api_route("/replay-stats", lambda: replay_stats(provider), methods=["GET"])
    app.add_api_route("/replay-approximate-log", lambda: provider.approximate_log, methods=["GET"])
    uvicorn.run(app, host="127.0.0.1", port=args.port, access_log=False)


if __name__ == "__main__":
    main()
