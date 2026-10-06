"""Realism pass (WP32): rewrite each synthetic email body in the style of real CRMArena-Pro emails with a free LLM.

Three modes of `Paraphraser`, used by the renderer through `text()`:
  off      the template text is used unchanged (default).
  collect  every (template id, slot values) is recorded to requests.jsonl; the template text is used.
  replay   the recorded cassette answers; a missing, rejected or invalid cassette falls back to the template
           text and is counted. Offline and deterministic: regeneration is byte-identical.
`run_pending` is the only place that calls a model (the worker provider: GHOST_MODEL, GHOST_FALLBACK_MODEL,
GHOST_LLM_MAX_RPM, 429 backoff, daily-cap breaker). It is resumable: a request with a cassette is skipped.

Cassette key = sha256 of {template_id, slots, seed}; `seed` is the generation seed plus the style id (base
export hash and prompt version), so changing the style pack never reuses stale cassettes.
Cassettes live in git-ignored data/; only the cassette manifest (counts and one hash) is committed.

    python -m synthetic.paraphrase run --dir data/synthetic/paraphrase_v1 --base <export> [--limit N]
    python -m synthetic.paraphrase status --dir data/synthetic/paraphrase_v1
    python -m synthetic.paraphrase rejudge --dir ... --base <export> --out-dir <new dir>   (validator changed: no model call, a new set and hash)
    python -m synthetic.paraphrase manifest --dir data/synthetic/paraphrase_v1 --out <manifest.json>
"""
from __future__ import annotations

import argparse
import hashlib
import json
import sys
import time
from collections import Counter
from collections.abc import Collection, Mapping, Sequence
from pathlib import Path
from typing import Any

from .paraphrase_check import WORD, check

PROMPT_VERSION = "p1"
MODES = ("off", "collect", "replay")
SYSTEM = ("You rewrite short B2B sales emails so they read like real emails from a CRM. Keep exactly the same meaning, "
          "intent and facts. Do not add any fact, name, company, date, number, amount, email address or link that is not "
          "in the original. Keep every protected string below exactly as written. Match the register, length and "
          "layout of the example emails. Reply with a JSON object {\"body\": \"<the rewritten email>\"} and nothing else.")
SCHEMA = {"type": "object", "properties": {"body": {"type": "string"}}, "required": ["body"], "additionalProperties": False}


def _sha(obj: Any) -> str:
    return hashlib.sha256(json.dumps(obj, sort_keys=True, ensure_ascii=False, separators=(",", ":")).encode("utf-8")).hexdigest()


def cassette_key(template_id: str, slots: Mapping[str, str], seed: str) -> str:
    return _sha({"template_id": template_id, "slots": dict(slots), "seed": seed})


def cassette_path(directory: Path, key: str) -> Path:
    return directory / "cassettes" / key[:2] / f"{key}.json"


def load_cassette(directory: Path, key: str) -> dict[str, Any] | None:
    path = cassette_path(directory, key)
    return json.loads(path.read_text(encoding="utf-8")) if path.exists() else None


def _save(directory: Path, cassette: Mapping[str, Any]) -> None:
    path = cassette_path(directory, cassette["key"])
    path.parent.mkdir(parents=True, exist_ok=True)
    tmp = path.with_suffix(".tmp")
    tmp.write_text(json.dumps(cassette, sort_keys=True, ensure_ascii=False, indent=1) + "\n", encoding="utf-8", newline="\n")
    tmp.replace(path)


def _requests(directory: Path) -> list[dict[str, Any]]:
    return [json.loads(line) for line in (directory / "requests.jsonl").read_text(encoding="utf-8").splitlines() if line]


class ParaphraseError(RuntimeError):
    """Replay found a missing, rejected or no-longer-valid cassette and template fallback was not allowed."""


class Paraphraser:
    def __init__(self, mode: str, directory: Path, seed: int, style_id: str = "", *, allow_fallback: bool = False,
                 vocab: Collection[str] = ()) -> None:
        if mode not in MODES:
            raise ValueError(f"unknown paraphrase mode {mode!r}")
        self.mode, self.dir = mode, Path(directory)
        self.seed = f"{seed}:{style_id}:{PROMPT_VERSION}"
        self.allow_fallback, self.vocab = allow_fallback, vocab
        self.requests: dict[str, dict[str, Any]] = {}
        self.used: dict[str, str] = {}  # cassette key -> sha256 of the cassette file actually used
        self.stats: Counter[str] = Counter()

    def text(self, template_id: str, slots: Mapping[str, str], rendered: str) -> str:
        """The email body to emit: the template text, or its paraphrase (re-validated here, never trusted)."""
        if self.mode == "off":
            return rendered
        key = cassette_key(template_id, slots, self.seed)
        if self.mode == "collect":
            self.requests.setdefault(key, {"key": key, "template_id": template_id, "slots": dict(slots),
                                           "seed": self.seed, "template_text": rendered})
            return rendered
        cassette = load_cassette(self.dir, key)
        if cassette is None:
            return self._fallback(f"no cassette {key[:12]} for {template_id}", "missing", rendered)
        state, reasons = judge({"template_text": rendered, "slots": dict(slots)}, cassette["text"], self.vocab)
        if state != "accepted":
            return self._fallback(f"cassette {key[:12]} ({template_id}) fails validation: {reasons[:2]}", "rejected_fallback", rendered)
        self.stats["paraphrased"] += 1
        self.used[key] = hashlib.sha256(cassette_path(self.dir, key).read_bytes()).hexdigest()
        return cassette["text"]

    def _fallback(self, why: str, counter: str, rendered: str) -> str:
        if not self.allow_fallback:
            raise ParaphraseError(f"{why}: complete the paraphrase run, or pass --allow-template-fallback")
        self.stats[counter] += 1
        return rendered

    def cassettes_sha256(self) -> str:
        """One hash over the cassettes this replay actually used (key and file hash, sorted)."""
        return hashlib.sha256("\n".join(f"{k} {v}" for k, v in sorted(self.used.items())).encode()).hexdigest()

    def flush_requests(self) -> int:
        """Append the collected requests not already on file; returns the number of requests on file."""
        path = self.dir / "requests.jsonl"
        path.parent.mkdir(parents=True, exist_ok=True)
        have = {r["key"] for r in _requests(self.dir)} if path.exists() else set()
        with path.open("a", encoding="utf-8", newline="\n") as f:
            for key, req in sorted(self.requests.items()):
                if key not in have:
                    f.write(json.dumps(req, sort_keys=True, ensure_ascii=False) + "\n")
        return len(have | set(self.requests))


def build_prompt(req: Mapping[str, Any], examples: Sequence[str]) -> str:
    protected = sorted({v for v in req["slots"].values() if v and v in req["template_text"]})
    shown = "\n\n".join(f"--- example {i + 1} ---\n{e}" for i, e in enumerate(examples))
    return (f"Example real emails (style only; never copy facts from them):\n{shown}\n\n"
            f"Protected strings (keep each exactly): {json.dumps(protected, ensure_ascii=False)}\n\n"
            f"Original email to rewrite:\n{req['template_text']}")


def pick_examples(pool: Sequence[str], key: str, n: int = 3, max_chars: int = 700) -> list[str]:
    """Deterministic style examples for a request: a hash-ordered slice of the real-email pool."""
    order = sorted(range(len(pool)), key=lambda i: hashlib.sha256(f"{key}:{i}".encode()).hexdigest())
    return [pool[i][:max_chars] for i in order[:n]]


def judge(req: Mapping[str, Any], text: str, vocab: Collection[str]) -> tuple[str, list[str]]:
    reasons = check(text, req["template_text"], req["slots"], vocab)
    return ("rejected" if reasons else "accepted"), reasons


def status(directory: Path) -> dict[str, Any]:
    keys = [r["key"] for r in _requests(directory)]
    have = [c for c in (load_cassette(directory, k) for k in keys) if c]
    rejected = [c for c in have if c["status"] != "accepted"]
    reasons = Counter(" ".join(r.split(" ")[:2]) for c in rejected for r in c["reasons"])
    return {"requests": len(keys), "cassettes": len(have), "accepted": len(have) - len(rejected), "rejected": len(rejected),
            "rejection_rate": round(len(rejected) / len(have), 4) if have else None, "remaining": len(keys) - len(have),
            "reject_reasons": dict(reasons)}


def run_pending(directory: Path, pool: Sequence[str], vocab: Collection[str], provider: Any, limit: int | None = None,
                clock=time.time) -> dict[str, Any]:
    """Call the model for every request without a cassette. Stops cleanly (resumable) on the daily cap."""
    from ghost_worker.errors import DailyCapError, ProviderError  # worker-py is on sys.path (main) or the test's path

    todo = [r for r in _requests(directory) if load_cassette(directory, r["key"]) is None]
    done: Counter[str] = Counter()
    started = clock()
    for req in todo[: limit if limit is not None else len(todo)]:
        try:
            result = provider.complete_json(system=SYSTEM, user=build_prompt(req, pick_examples(pool, req["key"])),
                                            schema=SCHEMA, schema_name="paraphrase")
        except DailyCapError as exc:
            done["stopped_daily_cap"] += 1
            print(f"daily cap reached; resume in about {exc.resume_in_s:.0f}s (rerun this command)", file=sys.stderr)
            break
        except ProviderError as exc:
            done["provider_error"] += 1
            print(f"provider error ({type(exc).__name__}); request left pending", file=sys.stderr)
            continue
        text = str(result.content.get("body", "")).strip()
        state, reasons = judge(req, text, vocab)
        _save(directory, {"key": req["key"], "template_id": req["template_id"], "slots": req["slots"], "seed": req["seed"],
                          "model": result.model, "status": state, "reasons": reasons, "text": text})
        done[state] += 1
        n = done["accepted"] + done["rejected"]
        if n % 50 == 0:
            st = status(directory)
            eta_h = st["remaining"] * (clock() - started) / n / 3600
            print(f"cassettes {st['cassettes']}/{st['requests']} rejected {st['rejected']} ({st['rejection_rate']}) "
                  f"eta {eta_h:.1f} h", flush=True)
    return {"processed": dict(done)}


def rejudge(directory: Path, vocab: Collection[str], out_dir: Path) -> dict[str, Any]:
    """Re-run the validator over every recorded paraphrase (no model call) and write the result to a NEW set of
    cassettes in `out_dir`; the original cassettes are never touched. Returns before/after rates and the new hash."""
    out_dir = Path(out_dir)
    if out_dir.resolve() == Path(directory).resolve() or (out_dir.exists() and any(out_dir.iterdir())):
        raise ValueError(f"rejudge writes a new set: {out_dir} must be a new or empty directory")
    before, changed = status(directory), 0
    out_dir.mkdir(parents=True, exist_ok=True)
    (out_dir / "requests.jsonl").write_bytes((directory / "requests.jsonl").read_bytes())
    for req in _requests(directory):
        c = load_cassette(directory, req["key"])
        if c is None:
            continue
        state, reasons = judge(req, c["text"], vocab)
        changed += (state, reasons) != (c["status"], c["reasons"])
        _save(out_dir, {**c, "status": state, "reasons": reasons, "rejudged": True})
    after = status(out_dir)
    return {"changed": changed, "before": {k: before[k] for k in ("cassettes", "rejected", "rejection_rate")},
            "after": {k: after[k] for k in ("cassettes", "rejected", "rejection_rate", "reject_reasons")},
            "cassettes_sha256": manifest(out_dir)["cassettes_sha256"]}


def manifest(directory: Path) -> dict[str, Any]:
    lines, models = [], Counter()
    for req in _requests(directory):
        path = cassette_path(directory, req["key"])
        lines.append(f"{req['key']} {hashlib.sha256(path.read_bytes()).hexdigest() if path.exists() else 'missing'}")
        if path.exists():
            models[load_cassette(directory, req["key"])["model"]] += 1
    return {"prompt_version": PROMPT_VERSION, **status(directory), "models": dict(models),
            "cassettes_sha256": hashlib.sha256("\n".join(sorted(lines)).encode()).hexdigest(),
            "note": "cassettes stay in git-ignored data/; only this manifest and its hash are committed"}


def real_pool_and_vocab(export: Path) -> tuple[list[str], set[str]]:
    """Style examples and the word list from the REAL EmailMessage rows (read-only; nothing is written)."""
    bodies = [str(r.get("TextBody") or "").strip() for r in json.loads((export / "EmailMessage.json").read_text(encoding="utf-8"))]
    counts = Counter(w for b in bodies for w in WORD.findall(b) if w[0].islower())
    return [b for b in bodies if 150 <= len(b) <= 900], {w for w, c in counts.items() if c >= 5}


def main(argv: Sequence[str] | None = None) -> int:
    p = argparse.ArgumentParser(prog="synthetic.paraphrase", description=__doc__.splitlines()[0])
    p.add_argument("cmd", choices=("run", "status", "manifest", "rejudge"))
    p.add_argument("--dir", required=True)
    p.add_argument("--base", help="CRMArena export (style examples and vocabulary; run only)")
    p.add_argument("--limit", type=int)
    p.add_argument("--out")
    p.add_argument("--out-dir", help="rejudge: a NEW directory for the re-judged set (originals are never rewritten)")
    a = p.parse_args(None if argv is None else list(argv))
    d = Path(a.dir)
    if a.cmd == "status":
        print(json.dumps(status(d), indent=1))
    elif a.cmd == "rejudge":
        print(json.dumps(rejudge(d, real_pool_and_vocab(Path(a.base))[1], Path(a.out_dir)), indent=1))
    elif a.cmd == "manifest":
        text = json.dumps(manifest(d), indent=1, sort_keys=True) + "\n"
        if a.out:
            Path(a.out).write_text(text, encoding="utf-8", newline="\n")
        print(text)
    else:
        sys.path.insert(0, str(Path(__file__).resolve().parents[2] / "worker-py"))
        from ghost_worker.llm.factory import build_provider
        from ghost_worker.settings import Settings

        pool, vocab = real_pool_and_vocab(Path(a.base))
        print(json.dumps({**run_pending(d, pool, vocab, build_provider(Settings(ghost_llm_mode="live")), a.limit),
                          **status(d)}, indent=1))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
