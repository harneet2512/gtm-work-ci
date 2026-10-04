from __future__ import annotations

import json
import logging

from ghost_worker.logging_setup import JsonFormatter, configure_logging


def make_record(**extra: object) -> logging.LogRecord:
    record = logging.LogRecord("ghost_worker.x", logging.INFO, __file__, 1, "hello %s", ("world",), None)
    for key, value in extra.items():
        setattr(record, key, value)
    return record


def test_formatter_emits_one_json_object_with_extras() -> None:
    line = JsonFormatter().format(make_record(activity_id="abc", claims=3))
    payload = json.loads(line)
    assert payload["event"] == "hello world"
    assert payload["level"] == "INFO" and payload["logger"] == "ghost_worker.x"
    assert (payload["activity_id"], payload["claims"]) == ("abc", 3)
    assert "\n" not in line


def test_formatter_records_exception_type_only() -> None:
    try:
        raise ValueError("secret detail")
    except ValueError:
        import sys
        record = make_record()
        record.exc_info = sys.exc_info()
    payload = json.loads(JsonFormatter().format(record))
    assert payload["exc_type"] == "ValueError"
    assert "secret detail" not in json.dumps(payload)


def test_configure_logging_is_idempotent() -> None:
    configure_logging()
    configure_logging()
    logger = logging.getLogger("ghost_worker")
    assert len(logger.handlers) == 1
    assert isinstance(logger.handlers[0].formatter, JsonFormatter)
