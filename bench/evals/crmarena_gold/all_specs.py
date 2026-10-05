"""Every spec of the CRMArena gold, in a fixed order (HAR-114 gold v2)."""
from __future__ import annotations

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
SPECS: list[dict] = [s for m in MODULES for s in m.SPECS]
IDS = [s["id"] for s in SPECS]
if len(IDS) != len(set(IDS)):
    raise ValueError("duplicate spec ids: " + ", ".join(sorted({i for i in IDS if IDS.count(i) > 1})))
BY_ID = {s["id"]: s for s in SPECS}
