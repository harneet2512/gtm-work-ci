"""Write the hand-written semantic-judge cassettes (cassettes/judges/unit/) from tests/judge_situations.py.

Run after any change to the judge prompts, rubrics or output schema (which changes cassette keys):
    cd worker-py && python scripts/seed_judge_cassettes.py
Each situation's judges run once through RecordingProvider(ScriptedJudgeProvider): no network, no model. The
scripted answers are hand-authored on neutral inputs and marked "hand_written": true.
"""
from __future__ import annotations

import json
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(ROOT))
sys.path.insert(0, str(ROOT / "tests"))

from judge_doubles import CATALOG, RUBRICS, ScriptedJudgeProvider  # noqa: E402
from judge_injection import INJECTION_RUN_ID  # noqa: E402
from judge_injection import SITUATIONS as INJECTION_SITUATIONS  # noqa: E402
from judge_situations import SITUATIONS, UNIT_RUN_ID  # noqa: E402

from ghost_worker.judges import run_suite  # noqa: E402
from ghost_worker.llm.fake_provider import RecordingProvider, model_family, write_cassette  # noqa: E402
from ghost_worker.settings import Settings  # noqa: E402

OUT = ROOT / "cassettes" / "judges" / "unit"
INJECTION_OUT = ROOT / "cassettes" / "judges" / "injection"  # HAR-125 regression cassettes (hand-written)


def seed(out: Path, situations: dict, run_id: str, family: str) -> None:
    """Replace `out` with one hand-written cassette per scripted judge answer."""
    out.mkdir(parents=True, exist_ok=True)
    for path in out.glob("*.json"):
        path.unlink()
    for situation in situations.values():
        scripted = ScriptedJudgeProvider({RUBRICS[n].schema_name: a for n, a in situation.answers.items()})
        provider = RecordingProvider(scripted, out, family)
        run_suite(situation.context, run_id, lambda trial: provider, RUBRICS, CATALOG,
                  eval_types=list(situation.answers))
        sys.stdout.write(f"{situation.name}: {len(situation.answers)} judges\n")
    for path in out.glob("*.json"):
        document = json.loads(path.read_text(encoding="utf-8"))
        write_cassette(out, document["key"], {**document, "hand_written": True})
    sys.stdout.write(f"{len(list(out.glob('*.json')))} cassettes in {out}\n")


def main() -> None:
    family = model_family(Settings(_env_file=None).ghost_model)
    seed(OUT, SITUATIONS, UNIT_RUN_ID, family)
    seed(INJECTION_OUT, INJECTION_SITUATIONS, INJECTION_RUN_ID, family)


if __name__ == "__main__":
    main()
