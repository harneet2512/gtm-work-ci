"""HAR-140 (WP36): operational diagnostics (tokens, latency, cost, tool counts: M1-M5) can never mutate GTM company
knowledge (HAR-97 canonical sections 2, 8 and 12). Registry level plus a source scan of every knowledge code path in
core-go and worker-py; the Go twin is core-go/internal/contracts/operational_isolation_test.go."""
from __future__ import annotations

import re
from pathlib import Path

from test_contracts import CONTRACTS, load_json

ROOT = CONTRACTS.parent
METRICS = load_json(CONTRACTS / "metrics" / "metric_registry.json")
REGISTRY = load_json(CONTRACTS / "evals" / "eval_registry.json")
EVALS = REGISTRY["evals"]
REGISTRY_METRICS = REGISTRY["metrics"]
OPERATIONAL_WORDS = re.compile(  # optional underscores so snake_case and Go CamelCase field names both match
    r"(input_?tokens|output_?tokens|prompt_?tokens|completion_?tokens|cached_?tokens|reasoning_?tokens|total_?tokens|"
    r"token_?count|tokens_?used|latency|cost|tool_?calls?|model_?calls?|retry_?count|retry_?rate|duration_?ms|"
    r"elapsed|(?<![a-z])usd(?![a-z]))", re.IGNORECASE)
KNOWLEDGE_GO_DIRS = ("knowledge", "knowledgestore", "knowledgebench")
KNOWLEDGE_PY_DIRS = ("knowledge_evals",)


def operational_hits(text: str) -> list[str]:
    return sorted({m.group(0).lower() for m in OPERATIONAL_WORDS.finditer(text)})


def knowledge_sources() -> list[Path]:
    """Go and Python files on a knowledge path: the knowledge packages, the worker knowledge evals, and any file importing them."""
    files: list[Path] = []
    for d in KNOWLEDGE_GO_DIRS:
        files += [p for p in (ROOT / "core-go" / "internal" / d).rglob("*.go") if not p.name.endswith("_test.go")]
    for d in KNOWLEDGE_PY_DIRS:
        files += list((ROOT / "worker-py" / "ghost_worker" / d).rglob("*.py"))
    importers = [p for p in (ROOT / "core-go").rglob("*.go")
                 if not p.name.endswith("_test.go") and "/internal/knowledge" in p.read_text(encoding="utf-8")]
    py_importers = [p for p in (ROOT / "worker-py" / "ghost_worker").rglob("*.py")
                    if "knowledge_evals" in p.read_text(encoding="utf-8") and "knowledge_evals" not in p.parts]
    return sorted({*files, *importers, *py_importers})


def test_the_scanner_would_catch_an_operational_metric_in_a_knowledge_path() -> None:
    assert operational_hits("conf := strengthen(k, run.input_tokens, latencyMs)") == ["input_tokens", "latency"]
    assert operational_hits("score := cost_usd / tool_calls") == ["cost", "tool_calls", "usd"]
    assert operational_hits("conf := Strengthen(k, run.InputTokens, cfg.ModelCalls, d.DurationMs)") == ["durationms", "inputtokens", "modelcalls"]
    assert operational_hits("RetryCount += TotalTokens + ToolCalls") == ["retrycount", "toolcalls", "totaltokens"]
    assert operational_hits("applicability precision and scope") == []


def test_no_knowledge_code_path_reads_tokens_latency_cost_or_tool_counts() -> None:
    sources = knowledge_sources()
    assert len(sources) >= 15, "the scan must actually cover the knowledge packages"
    offenders = {str(p.relative_to(ROOT)): hits for p in sources if (hits := operational_hits(p.read_text(encoding="utf-8")))}
    assert not offenders, f"operational metric feeding a knowledge path: {offenders}"


def test_operational_diagnostics_never_allow_knowledge_mutation_in_the_registry() -> None:
    ops = METRICS["operational_diagnostics"]
    assert len(ops) == 5 and all(o["knowledge_mutation_allowed"] is False and o["class"] == "operational" for o in ops)
    for o in ops:
        assert not {"knowledge", "learning", "eval_criteria", "decision_guidance"} & set(o["consumers"]), o["id"]


def test_operational_evals_are_not_knowledge_evidence_and_no_knowledge_eval_is_operational() -> None:
    assert not [e for e in EVALS if e["class"] == "operational"], "M1-M5 are metrics, never evals"
    operational = REGISTRY_METRICS
    assert len(operational) == 5 and not any(e["knowledge_mutation_allowed"] for e in operational)
    knowledge = [e for e in EVALS if e["family"] in ("E15", "E16", "E17")]
    assert knowledge and all(e["class"] == "behavioral" and 20 not in e["surfaces"] for e in knowledge)


def test_no_knowledge_metric_takes_an_operational_quantity_as_input() -> None:
    knowledge_layers = ("knowledge_L4", "edit_propagation", "meta_L5")
    for m in (x for x in METRICS["metrics"] if x["layer"] in knowledge_layers):
        text = " ".join([*m["inputs"]["data"], *m["inputs"]["gold"], str(m["formula"]), m["definition"]])
        assert not operational_hits(text), (m["id"], operational_hits(text))
