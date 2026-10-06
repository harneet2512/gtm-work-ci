"""Validators for the /v1/draft request/response bodies taken straight from contracts/openapi/worker.yaml."""
from __future__ import annotations

from typing import Any

import yaml
from jsonschema import Draft202012Validator, FormatChecker
from referencing import Resource
from referencing.jsonschema import DRAFT202012

from test_contracts import CONTRACTS, REGISTRY

_SCHEMA_PREFIX = "https://ghost.local/contracts/"
_SPEC_ID = "https://ghost.local/openapi/worker.yaml"


def _rewrite_ref(ref: str) -> str:
    if ref.startswith("../schemas/"):
        return _SCHEMA_PREFIX + ref.removeprefix("../schemas/")
    if ref.startswith("#/"):  # a pointer into the spec itself (the request schemas reuse /v1/draft's)
        return _SPEC_ID + ref
    return ref


def _rewrite(node: Any) -> Any:
    """Point '../schemas/x.json#...' refs at the registry's absolute ids so jsonschema can resolve them."""
    if isinstance(node, dict):
        return {k: _rewrite_ref(v) if k == "$ref" and isinstance(v, str) else _rewrite(v) for k, v in node.items()}
    if isinstance(node, list):
        return [_rewrite(v) for v in node]
    return node


def _spec() -> dict:
    return yaml.safe_load((CONTRACTS / "openapi" / "worker.yaml").read_text(encoding="utf-8"))


def _validator(schema: dict) -> Draft202012Validator:
    registry = REGISTRY.with_resource(_SPEC_ID, Resource.from_contents(_rewrite(_spec()), default_specification=DRAFT202012))
    return Draft202012Validator(_rewrite(schema), registry=registry, format_checker=FormatChecker())


def draft_request_validator() -> Draft202012Validator:
    body = _spec()["paths"]["/v1/draft"]["post"]["requestBody"]["content"]["application/json"]["schema"]
    return _validator(body)


def draft_response_validator() -> Draft202012Validator:
    body = _spec()["paths"]["/v1/draft"]["post"]["responses"]["200"]["content"]["application/json"]["schema"]
    return _validator(body)


def extract_request_validator() -> Draft202012Validator:
    body = _spec()["paths"]["/v1/extract"]["post"]["requestBody"]["content"]["application/json"]["schema"]
    return _validator(body)


def extract_response_validator() -> Draft202012Validator:
    body = _spec()["paths"]["/v1/extract"]["post"]["responses"]["200"]["content"]["application/json"]["schema"]
    return _validator(body)


def request_validator(path: str) -> Draft202012Validator:
    body = _spec()["paths"][path]["post"]["requestBody"]["content"]["application/json"]["schema"]
    return _validator(body)


def response_validator(path: str) -> Draft202012Validator:
    body = _spec()["paths"][path]["post"]["responses"]["200"]["content"]["application/json"]["schema"]
    return _validator(body)
