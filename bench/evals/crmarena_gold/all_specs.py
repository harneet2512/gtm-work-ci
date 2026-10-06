"""Every spec of the CRMArena gold, in a fixed order (HAR-114 gold v2).

Specs cut from a demo-partition deal (bench/data/account_split.json: MedTech Advances and EcoLite Innovations, the
accounts the demo shows) are not gold: they leaked the second Play's test episode into the cases. They stay written in
the specs_*.py modules as the record of what was removed (EXCLUDED_IDS) and are never built.
"""
from __future__ import annotations

import json
from pathlib import Path

from . import specs_d1
from . import specs_d2
from . import specs_d3
from . import specs_d4
from . import specs_d5
from . import specs_d6
from . import specs_d7
from . import specs_d8
from . import specs_d9
from . import specs_d11
from . import specs_x

MODULES = (specs_d1, specs_d2, specs_d3, specs_d4, specs_d5, specs_d6, specs_d7, specs_d8, specs_d9, specs_d11, specs_x)
DEMO_DEALS = frozenset(json.loads((Path(__file__).resolve().parents[3] / "bench" / "data" / "account_split.json")
                                   .read_text(encoding="utf-8"))["partitions"]["demo"]["deal_ids"])
ALL_SPECS: list[dict] = [s for m in MODULES for s in m.SPECS]
SPECS: list[dict] = [s for s in ALL_SPECS if s["deal"] not in DEMO_DEALS]
EXCLUDED_IDS: list[str] = [s["id"] for s in ALL_SPECS if s["deal"] in DEMO_DEALS]
IDS = [s["id"] for s in SPECS]
if len(IDS) != len(set(IDS)):
    raise ValueError("duplicate spec ids: " + ", ".join(sorted({i for i in IDS if IDS.count(i) > 1})))
BY_ID = {s["id"]: s for s in SPECS}
