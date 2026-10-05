"""Real-vs-synthetic distinguishability (WP32 step 2, precondition 6): can a classifier tell a generated email
from a real CRMArena-Pro email by what an agent sees?

Units are emails: the export's EmailMessage rows (real) and the generated email SourceEvents (synthetic).
L2 logistic regression on standardised features, 5-fold cross-validation with folds grouped by account,
reported as AUC (0.5 = indistinguishable) for nested feature sets: text only; text + timing; text + timing +
thread shape (subject 'Re:', any cc). Identifiers are not features; the id tell is reported separately
(synthetic ids continue after the real maximum by construction). Aggregates only: no row leaves this module.

Run: python -m synthetic.distinguish --base <wp31 export> --generated data/synthetic/v1 --out <report.json>
"""
from __future__ import annotations

import argparse
import json
import math
import re
from collections.abc import Sequence
from datetime import datetime, timezone
from pathlib import Path
from typing import Any

from .base import is_rep, when
from .power_sim import auc, fit_logistic
from .seeding import stream

FOLDS = 5
WORD = re.compile(r"[A-Za-z']+")
TEXT = ("log_words", "log_chars", "lines", "paragraphs", "mean_word_len", "type_token", "commas_per_word",
        "questions", "digit_share", "greeting_hi", "greeting_dear", "signoff_best_regards", "signoff_kind",
        "subject_words", "outbound")
TIMING = ("hour_sin", "hour_cos", "minute_nonzero", "weekend")
THREAD = ("subject_re", "has_cc")
SETS = {"text": TEXT, "text_timing": TEXT + TIMING, "text_timing_thread": TEXT + TIMING + THREAD}


def features(subject: str, body: str, at: datetime, outbound: bool, has_cc: bool) -> dict[str, float]:
    words = WORD.findall(body)
    n = max(1, len(words))
    hour = at.hour + at.minute / 60
    return {
        "log_words": math.log1p(len(words)), "log_chars": math.log1p(len(body)), "lines": body.count("\n"),
        "paragraphs": body.count("\n\n"), "mean_word_len": sum(map(len, words)) / n,
        "type_token": len({w.lower() for w in words}) / n, "commas_per_word": body.count(",") / n,
        "questions": body.count("?"), "digit_share": sum(c.isdigit() for c in body) / max(1, len(body)),
        "greeting_hi": float(body.lstrip().startswith("Hi")), "greeting_dear": float(body.lstrip().startswith("Dear")),
        "signoff_best_regards": float("Best regards" in body), "signoff_kind": float("Kind regards" in body),
        "subject_words": len(subject.split()), "outbound": float(outbound),
        "hour_sin": math.sin(2 * math.pi * hour / 24), "hour_cos": math.cos(2 * math.pi * hour / 24),
        "minute_nonzero": float(at.minute != 0), "weekend": float(at.weekday() >= 5),
        "subject_re": float(subject.lower().startswith("re:")), "has_cc": float(has_cc),
    }


def real_emails(export: Path) -> list[tuple[str, dict[str, float]]]:
    opps = {o["Id"]: o["AccountId"] for o in json.loads((export / "Opportunity.json").read_text(encoding="utf-8"))}
    out = []
    for e in json.loads((export / "EmailMessage.json").read_text(encoding="utf-8")):
        acct = opps.get(e.get("RelatedToId") or "")
        if acct:
            out.append((acct, features(e.get("Subject") or "", e.get("TextBody") or "", when(e["MessageDate"]),
                                       is_rep(e.get("FromAddress")), bool(e.get("CcAddress")))))
    return out


def synthetic_emails(generated: Path) -> list[tuple[str, dict[str, float]]]:
    out = []
    for path in sorted((generated / "events").rglob("*.json")):
        for e in json.loads(path.read_text(encoding="utf-8")):
            if e["source_system"] != "email":
                continue
            p = e["payload"]
            at = datetime.strptime(p["date"], "%Y-%m-%dT%H:%M:%SZ").replace(tzinfo=timezone.utc)
            out.append((path.parent.name, features(p["subject"], p["body_text"], at, p["direction"] == "outbound",
                                                   bool(p.get("cc")))))
    return out


def _standardise(xs: list[list[float]], train: Sequence[int]) -> list[list[tuple[int, float]]]:
    d = len(xs[0])
    mean = [sum(xs[i][j] for i in train) / len(train) for j in range(d)]
    sd = [math.sqrt(sum((xs[i][j] - mean[j]) ** 2 for i in train) / len(train)) or 1.0 for j in range(d)]
    return [[(0, 1.0)] + [(j + 1, (x[j] - mean[j]) / sd[j]) for j in range(d) if x[j] != mean[j]] for x in xs]


def cv_auc(rows: list[tuple[str, dict[str, float]]], labels: list[bool], names: Sequence[str], seed: int) -> float:
    """Mean of the per-fold AUCs (a fold's intercept never shifts another fold's ranking); folds are accounts,
    assigned by a seeded shuffle, so no account is in both the training and the test part."""
    accounts = sorted({a for a, _ in rows})
    order = stream(seed, "distinguish", "folds").shuffled(accounts)
    fold = {a: i % FOLDS for i, a in enumerate(order)}
    xs = [[f[n] for n in names] for _, f in rows]
    per_fold = []
    for k in range(FOLDS):
        train = [i for i, (a, _) in enumerate(rows) if fold[a] != k]
        test = [i for i, (a, _) in enumerate(rows) if fold[a] == k]
        if len({labels[i] for i in train}) < 2 or len({labels[i] for i in test}) < 2:
            continue
        z = _standardise(xs, train)
        w = fit_logistic([z[i] for i in train], [labels[i] for i in train], len(names) + 1, l2=1.0, iters=8)
        per_fold.append(auc([sum(w[j] * v for j, v in z[i]) for i in test], [labels[i] for i in test]))
    return sum(per_fold) / len(per_fold) if per_fold else 0.5


def report(export: Path, generated: Path, seed: int) -> dict[str, Any]:
    real, synth = real_emails(export), synthetic_emails(generated)
    rows, labels = real + synth, [False] * len(real) + [True] * len(synth)
    share = lambda rs, k: round(sum(f[k] for _, f in rs) / max(1, len(rs)), 4)  # noqa: E731
    return {
        "unit": "email", "real_emails": len(real), "synthetic_emails": len(synth), "folds": f"{FOLDS}, grouped by account",
        "auc": {name: round(cv_auc(rows, labels, names, seed), 4) for name, names in SETS.items()},
        "single_feature_tells": {k: {"real": share(real, k), "synthetic": share(synth, k)}
                                 for k in ("has_cc", "subject_re", "minute_nonzero", "weekend", "outbound")},
        "id_tell": "every synthetic id sorts after the real maximum of its prefix (records.sequential_id), so an id "
                   "rank separates real from synthetic perfectly; ids are not features above",
    }


def main(argv: Sequence[str] | None = None) -> int:
    p = argparse.ArgumentParser(prog="synthetic.distinguish", description=__doc__.splitlines()[0])
    p.add_argument("--base", required=True)
    p.add_argument("--generated", required=True)
    p.add_argument("--out", required=True)
    p.add_argument("--seed", type=int, default=20261002)
    args = p.parse_args(argv)
    rep = report(Path(args.base), Path(args.generated), args.seed)
    Path(args.out).write_text(json.dumps(rep, indent=1, sort_keys=True) + "\n", encoding="utf-8", newline="\n")
    print(json.dumps(rep["auc"]))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
