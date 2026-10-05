"""Read-only mapping type for fields of frozen models (a frozen model must not expose a mutable dict)."""
from __future__ import annotations

from collections.abc import Mapping
from types import MappingProxyType
from typing import Annotated, Any

from pydantic import AfterValidator, PlainSerializer


def _freeze(value: Mapping[str, Any]) -> Mapping[str, Any]:
    return MappingProxyType(dict(value))


FrozenMap = Annotated[Mapping[str, Any], AfterValidator(_freeze),
                      PlainSerializer(dict, return_type=dict[str, Any])]
