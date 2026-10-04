"""Blind A/B/C knowledge-uplift experiment (HAR-128 WP30, HAR-129 'Required knowledge-uplift experiment').

Arm A: same account state, no learned knowledge. Arm B: plus applicable learned knowledge. Arm C: plus irrelevant
knowledge. The primary metric is deterministic: how the hidden rules of the labelled synthetic layer (HAR-131) rate
the action the account agent chose. Bench-only: nothing under core-go/ or worker-py/ghost_worker imports this package.

It is `python -m bench.uplift` (not `abc`: that name shadows the standard library module on any sys.path that has
bench/ first, and the existing bench tests put bench/ there). The Go half (the real orchestrator path) is
`ghostctl abc-arms`.
"""
