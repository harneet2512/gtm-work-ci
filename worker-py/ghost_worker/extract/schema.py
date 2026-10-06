"""JSON Schema for the model's output (strict-mode friendly: closed objects, every key required)."""
from __future__ import annotations

from typing import Any, get_args

from ..models import FieldPath, Role

OUTPUT_SCHEMA_NAME = "extract_claims_v1"

_NULLABLE_STRING: dict[str, Any] = {"type": ["string", "null"]}

OUTPUT_SCHEMA: dict[str, Any] = {
    "type": "object",
    "additionalProperties": False,
    "required": ["claims"],
    "properties": {
        "claims": {
            "type": "array",
            "items": {
                "type": "object",
                "additionalProperties": False,
                "required": ["field_path", "value", "confidence", "evidence_quote",
                             "speaker_identity", "subject_identity", "role", "due_at"],
                "properties": {
                    "field_path": {"type": "string", "enum": list(get_args(FieldPath))},
                    "value": {"type": "string"},
                    "confidence": {"type": "number"},
                    "evidence_quote": {"type": "string"},
                    "speaker_identity": _NULLABLE_STRING,
                    "subject_identity": _NULLABLE_STRING,
                    "role": {"type": ["string", "null"], "enum": [*get_args(Role), None]},
                    "due_at": _NULLABLE_STRING,
                },
            },
        },
    },
}
