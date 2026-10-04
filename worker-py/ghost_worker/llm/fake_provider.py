"""Cassette replay (FakeProvider) and record (RecordingProvider). Replay never touches the network."""
from __future__ import annotations

import hashlib
import json
import os
import tempfile
import logging
from pathlib import Path
from typing import Any, TypeVar

from ..errors import CassetteNotFoundError, InvalidModelOutputError
from .provider import LLMProvider, LLMResult, TurnResult


log = logging.getLogger(__name__)

R = TypeVar("R", LLMResult, TurnResult)


def model_family(model: str) -> str:
    """'openrouter/deepseek/deepseek-v4-flash:free' -> 'deepseek-v4-flash' (stable across routing prefixes)."""
    return model.rsplit("/", 1)[-1].split(":", 1)[0]


def _canonical(obj: object) -> str:
    return json.dumps(obj, sort_keys=True, separators=(",", ":"), ensure_ascii=False)


def schema_sha256(schema: dict[str, Any]) -> str:
    return hashlib.sha256(_canonical(schema).encode("utf-8")).hexdigest()


def cassette_key(family: str, system: str, user: str, schema_name: str, schema: dict[str, Any]) -> str:
    payload = {"model_family": family, "system": system, "user": user, "schema_name": schema_name,
               "schema_sha256": schema_sha256(schema)}
    return hashlib.sha256(_canonical(payload).encode("utf-8")).hexdigest()


def turn_cassette_key(family: str, system: str, messages: list[dict[str, Any]], tools: list[dict[str, Any]],
                      schema_name: str, schema: dict[str, Any]) -> str:
    """Key of a tool-calling turn: the whole transcript so far, the tool set and the final-answer schema."""
    return cassette_key(family, system, _canonical(messages), schema_name, {"tools": tools, "final": schema})


def _cassette_path(directory: Path, key: str) -> Path:
    return directory / f"{key}.json"


class FakeProvider:
    """Replays hand-written or recorded cassettes from `directory`."""

    def __init__(self, directory: Path, family: str) -> None:
        self.directory = Path(directory)
        self.family = family

    def complete_json(self, *, system: str, user: str, schema: dict[str, Any], schema_name: str) -> LLMResult:
        key = cassette_key(self.family, system, user, schema_name, schema)
        return self._replay(key, schema_name, LLMResult)

    def complete_turn(self, *, system: str, messages: list[dict[str, Any]], tools: list[dict[str, Any]],
                      schema: dict[str, Any], schema_name: str) -> TurnResult:
        key = turn_cassette_key(self.family, system, messages, tools, schema_name, schema)
        return self._replay(key, schema_name, TurnResult)

    def _replay(self, key: str, schema_name: str, result_type: type[R]) -> R:
        path = _cassette_path(self.directory, key)
        if not path.is_file():
            raise CassetteNotFoundError(
                f"no cassette {key} in {self.directory} (model_family={self.family}, schema={schema_name}); "
                "replay mode never calls the network - re-record with GHOST_LLM_MODE=record")
        try:
            response = json.loads(path.read_text(encoding="utf-8"))["response"]
            return result_type.model_validate(response)
        except (ValueError, KeyError, TypeError) as exc:
            raise InvalidModelOutputError(f"cassette {key} is unreadable: {type(exc).__name__}") from None


class RecordingProvider:
    """Calls a real provider and persists each exchange as a cassette (atomic write)."""

    def __init__(self, upstream: LLMProvider, directory: Path, family: str, *, reuse: bool = False) -> None:
        self.upstream = upstream
        self.directory = Path(directory)
        self.family = family
        self.reuse = reuse  # an existing cassette answers instead of a new provider call (resumable recording)
        self._replay = FakeProvider(self.directory, family)
        log.warning("cassette RECORD mode: recordings contain the full prompt and may include customer text; "
                    "review before committing", extra={"cassette_dir": str(self.directory)})

    def complete_json(self, *, system: str, user: str, schema: dict[str, Any], schema_name: str) -> LLMResult:
        key = cassette_key(self.family, system, user, schema_name, schema)
        if self.reuse and _cassette_path(self.directory, key).is_file():
            return self._replay.complete_json(system=system, user=user, schema=schema, schema_name=schema_name)
        result = self.upstream.complete_json(system=system, user=user, schema=schema, schema_name=schema_name)
        document = {
            "key": key,
            "request": {"model_family": self.family, "system": system, "user": user, "schema_name": schema_name,
                        "schema_sha256": schema_sha256(schema)},
            "response": result.model_dump(mode="json"),  # response.model is the model that actually answered
        }
        write_cassette(self.directory, key, document)
        return result

    def complete_turn(self, *, system: str, messages: list[dict[str, Any]], tools: list[dict[str, Any]],
                      schema: dict[str, Any], schema_name: str) -> TurnResult:
        key = turn_cassette_key(self.family, system, messages, tools, schema_name, schema)
        if self.reuse and _cassette_path(self.directory, key).is_file():
            return self._replay.complete_turn(system=system, messages=messages, tools=tools, schema=schema, schema_name=schema_name)
        result = self.upstream.complete_turn(system=system, messages=messages, tools=tools,
                                             schema=schema, schema_name=schema_name)
        document = {
            "key": key,
            "request": {"model_family": self.family, "system": system, "messages": messages,
                        "tools_sha256": schema_sha256({"tools": tools}), "schema_name": schema_name,
                        "schema_sha256": schema_sha256(schema)},
            "response": result.model_dump(mode="json"),
        }
        write_cassette(self.directory, key, document)
        return result


def write_cassette(directory: Path, key: str, document: dict[str, Any]) -> Path:
    directory.mkdir(parents=True, exist_ok=True)
    target = _cassette_path(directory, key)
    fd, tmp_name = tempfile.mkstemp(dir=directory, prefix=f".{key}.", suffix=".tmp")
    try:
        with os.fdopen(fd, "w", encoding="utf-8", newline="\n") as handle:
            json.dump(document, handle, indent=2, ensure_ascii=False)
            handle.write("\n")
        os.replace(tmp_name, target)
    except BaseException:
        Path(tmp_name).unlink(missing_ok=True)
        raise
    return target
