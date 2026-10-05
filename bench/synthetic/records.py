"""Record markers, ids and output layout of the synthetic layer (spec §e, §f).

Synthetic ids are shaped exactly like the CRMArena-Pro Salesforce ids (3-char key prefix, the
org's 'Wt' pod chars, a zero-padded body, the 3-char case-safe checksum), derived from a hash so
they are deterministic. Nothing in an id, a name or an email address reveals that a record is
synthetic: the marker lives only in the SourceEvent envelope (origin/provenance), which core
keeps on source_events and never carries onto activities, people or evidence.
"""
from __future__ import annotations

import hashlib
from collections.abc import Mapping
from pathlib import Path
from typing import Any

ORIGIN = "synthetic"
PROVENANCE = "synthetic:v1"

# Salesforce key prefixes of the objects the layer creates.
KEY_PREFIX = {"Contact": "003", "Opportunity": "006", "EmailMessage": "02s", "Note": "002", "Task": "00T", "Quote": "0Q0"}
POD = "Wt"  # every CRMArena-Pro B2B id carries it, e.g. 00TWt000002zEpkMAE
BASE62 = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"
_BASE62 = BASE62
_CHECKSUM = "ABCDEFGHIJKLMNOPQRSTUVWXYZ012345"

# Output layout under the git-ignored data/ directory. Base data is only ever read.
OUT_ROOT = Path("data") / "synthetic" / "v1"
EVENTS_DIR = OUT_ROOT / "events"    # SourceEvent JSON files: the only thing replay ingests
LABELS_DIR = OUT_ROOT / "labels"    # ground truth (roles, planted features, outcomes): scorer-only
MANIFEST = OUT_ROOT / "manifest.json"  # seed, rule-file hash, base-data hash, counts, QA gates


def mark(record: Mapping[str, Any]) -> dict[str, Any]:
    """Return a new SourceEvent envelope carrying origin=synthetic and provenance=synthetic:v1.

    Refuses to relabel a record that already declares another origin or provenance.
    """
    for key, want in (("origin", ORIGIN), ("provenance", PROVENANCE)):
        have = record.get(key)
        if have is not None and have != want:
            raise ValueError(f"record already has {key}={have!r}; refusing to mark it {want!r}")
    return {**record, "origin": ORIGIN, "provenance": PROVENANCE}


def sf_checksum(id15: str) -> str:
    """Salesforce case-safe suffix: one char per 5-char chunk, bit i set when char i is upper case."""
    if len(id15) != 15:
        raise ValueError("a Salesforce id body has 15 characters")
    return "".join(
        _CHECKSUM[sum(1 << j for j, ch in enumerate(id15[k:k + 5]) if "A" <= ch <= "Z")] for k in (0, 5, 10)
    )


def sf_id(object_type: str, *parts: str) -> str:
    """Deterministic 18-char id with the object's key prefix, e.g. sf_id('Contact', acct, 'entrant', '1')."""
    digest = int.from_bytes(hashlib.sha256("\x1f".join((object_type, *parts)).encode("utf-8")).digest()[:8], "big")
    body = ""
    for _ in range(6):
        digest, r = divmod(digest, 62)
        body = _BASE62[r] + body
    id15 = KEY_PREFIX[object_type] + POD + "0000" + body
    return id15 + sf_checksum(id15)


def id_number(sf: str) -> int:
    """The record counter of a Salesforce id: its 10 body chars after the prefix and pod, in base 62."""
    n = 0
    for ch in sf[5:15]:
        n = n * 62 + BASE62.index(ch)
    return n


def sequential_id(object_type: str, after_body: str, rank: int, gap: int, salt: str) -> str:
    """The rank-th synthetic id of a type, allocated AFTER the highest real id (after_body = its 12 chars
    after the prefix) with the real median gap plus a hash jitter, so synthetic ids continue the
    real sequence instead of looking uniformly random. The generator ranks objects by (time, key)."""
    jitter = int.from_bytes(hashlib.sha256(f"{object_type}\x1f{salt}".encode("utf-8")).digest()[:4], "big") % max(gap, 1)
    n = id_number("000" + after_body) + (rank + 1) * max(gap, 1) + jitter
    body = ""
    for _ in range(10):
        n, r = divmod(n, 62)
        body = BASE62[r] + body
    id15 = KEY_PREFIX[object_type] + after_body[:2] + body
    return id15 + sf_checksum(id15)


def message_id(email_message_id: str, domain: str) -> str:
    """RFC 5322 Message-ID for an EmailMessage id. WP31 must render base emails the same way, so a
    synthetic reply's header is indistinguishable from a base one (spec open question 9)."""
    return f"<{email_message_id}@{domain}>"
