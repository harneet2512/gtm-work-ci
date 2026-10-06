"""Operate the CRMArena paid-extraction run on a LOCAL Postgres (HAR-104 / HAR-130).

    pip install "psycopg[binary]"
    DATABASE_URL=postgres://...@127.0.0.1:.../ghost python bench/data/crmarena_extraction_run.py pilot-seed
    DATABASE_URL=...                                python bench/data/crmarena_extraction_run.py release
    OPENROUTER_API_KEY=...                          python bench/data/crmarena_extraction_run.py spend

Run order: `ghostctl migrate up`, `ghostctl import-crmarena data/crmarena_b2b`, `pilot-seed`, start the
worker (GHOST_LLM_MODE=record) and one or more `core` processes, wait for the jobs to drain, measure the
cost per event, then `release` and let the cores drain again.

pilot-seed draws a 5% sample of the extractable emails (stratified by activity type, seeded) and caches
an empty answer, model `held-out`, for every other one. The coalescer then pays the model only for the
sample. `release` removes the placeholders and queues a full-timeline recompute for every account;
answers already paid for stay cached, so nothing is bought twice.

Refuses any database that is not on localhost. The OpenRouter key is read from the environment, sent
only to openrouter.ai and never printed.
"""
from __future__ import annotations

import argparse
import json
import os
import random
import sys
import urllib.request
from pathlib import Path
from urllib.parse import urlparse

sys.path.insert(0, str(Path(__file__).resolve().parent))
from crmarena_extraction_metrics import stratified_sample, utf8_connection  # noqa: E402

ROOT = Path(__file__).resolve().parents[2]
EXTRACTOR_VERSION = "extract-v4"
EXTRACTABLE_TYPES = ("EmailReceived", "EmailReply", "EmailSent")
HELD_OUT = "held-out"
SAMPLE_FRACTION = 0.05
SAMPLE_SEED = 20261002
SAMPLE_FILE = ROOT / "data" / "crmarena_extraction" / "pilot_sample.json"
LOCAL_HOSTS = frozenset({"127.0.0.1", "localhost", "::1"})
KEY_URL = "https://openrouter.ai/api/v1/key"


def connect():
    url = os.environ.get("DATABASE_URL", "")
    host = urlparse(url).hostname
    if host not in LOCAL_HOSTS:
        raise SystemExit("refusing to run: DATABASE_URL must point at localhost (embedded Postgres), never a shared database")
    return utf8_connection(url)


def pilot_seed() -> dict:
    with connect() as conn:
        rows = conn.execute(
            "SELECT activity_type, id::text FROM activities WHERE activity_type = ANY(%s) AND btrim(coalesce(body_text, '')) <> ''",
            (list(EXTRACTABLE_TYPES),)).fetchall()
        strata: dict[str, list[str]] = {}
        for kind, activity_id in rows:
            strata.setdefault(kind, []).append(activity_id)
        sample = stratified_sample(strata, SAMPLE_FRACTION, random.Random(SAMPLE_SEED))
        chosen = {i for ids in sample.values() for i in ids}
        held = [i for ids in strata.values() for i in ids if i not in chosen]
        placeholder = json.dumps({"claims": [], "dropped": 0, "model": HELD_OUT, "extractor_version": EXTRACTOR_VERSION})
        with conn.cursor() as cur:
            cur.executemany(
                "INSERT INTO extraction_cache (activity_id, extractor_version, model, output) VALUES (%s::uuid, %s, %s, %s::jsonb) "
                "ON CONFLICT DO NOTHING", [(i, EXTRACTOR_VERSION, HELD_OUT, placeholder) for i in held])
        conn.commit()
    summary = {"seed": SAMPLE_SEED, "fraction": SAMPLE_FRACTION,
               "population": {k: len(v) for k, v in strata.items()},
               "sample": {k: len(v) for k, v in sample.items()}, "held_out": len(held)}
    SAMPLE_FILE.parent.mkdir(parents=True, exist_ok=True)
    SAMPLE_FILE.write_text(json.dumps({**summary, "ids": sample}, indent=1), encoding="utf-8")
    return summary


def release() -> dict:
    with connect() as conn:
        deleted = conn.execute("DELETE FROM extraction_cache WHERE model = %s", (HELD_OUT,)).rowcount
        queued = conn.execute(
            """INSERT INTO recompute_jobs (account_id, due_at, first_enqueued_at, activity_ids)
               SELECT account_id, now(), now(), array_agg(id) FROM activities WHERE account_id IS NOT NULL GROUP BY account_id
               ON CONFLICT (account_id) WHERE claimed_at IS NULL DO UPDATE
                 SET activity_ids = EXCLUDED.activity_ids, due_at = EXCLUDED.due_at, attempts = 0, last_error = NULL""").rowcount
        conn.commit()
    return {"placeholders_removed": deleted, "jobs_queued": queued}


def spend() -> dict:
    """Total credits used on the key so far, from OpenRouter's own meter (the number we are capped on)."""
    key = os.environ.get("OPENROUTER_API_KEY", "")
    if not key:
        raise SystemExit("OPENROUTER_API_KEY is not set")
    request = urllib.request.Request(KEY_URL, headers={"Authorization": f"Bearer {key}"})
    with urllib.request.urlopen(request, timeout=30) as response:  # noqa: S310 - fixed https URL
        data = json.load(response)["data"]
    return {"usage_usd": data.get("usage"), "limit": data.get("limit")}


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("command", choices=("pilot-seed", "release", "spend"))
    args = parser.parse_args()
    print(json.dumps({"pilot-seed": pilot_seed, "release": release, "spend": spend}[args.command](), indent=1))


if __name__ == "__main__":
    main()
