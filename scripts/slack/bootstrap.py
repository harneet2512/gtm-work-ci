"""Slack app bootstrap for the GTM AI demo (HAR-138).

Creates or updates the Slack app from contracts/slack/manifest.yaml through the apps.manifest.* API,
resolves (or creates) #gtm-ai-demo, and runs a Socket Mode + post/update smoke test.

Secrets are never printed. Tokens are read from the process environment and the git-ignored repo-root .env
only (no credential file is read), and runtime values are written only to that .env.

Usage:
  python scripts/slack/bootstrap.py validate
  python scripts/slack/bootstrap.py apply      # update the app in .env (SLACK_APP_ID) or create one
  python scripts/slack/bootstrap.py status     # which runtime credentials are present (yes/no only)
  python scripts/slack/bootstrap.py channel    # resolve/create #gtm-ai-demo, join, store SLACK_CHANNEL_ID
  python scripts/slack/bootstrap.py smoke      # Socket Mode hello + post + update + delete its own test message
  python scripts/slack/bootstrap.py read [N]   # print the last N messages in #gtm-ai-demo (default 20)

Nothing here posts free text into #gtm-ai-demo (HAR-129 demo boundary): the audience channel carries Cliff's
messages from the core outbox and nothing else.
"""
from __future__ import annotations

import json
import os
import sys
import urllib.parse
import urllib.request
from pathlib import Path

import yaml

ROOT = Path(__file__).resolve().parents[2]
MANIFEST = ROOT / "contracts" / "slack" / "manifest.yaml"
ENV_FILE = ROOT / ".env"
CHANNEL_NAME = "gtm-ai-demo"
LEGACY_CHANNEL_NAMES = ("ghost-demo",)
ENV_KEYS = {"bot": "SLACK_BOT_TOKEN", "app": "SLACK_APP_TOKEN", "config": "SLACK_CONFIG_TOKEN"}


class BootstrapError(RuntimeError):
    """A Slack API or configuration failure. Messages never contain secrets."""


def read_env() -> dict[str, str]:
    if not ENV_FILE.exists():
        return {}
    out = {}
    for line in ENV_FILE.read_text(encoding="utf-8").splitlines():
        if "=" in line and not line.lstrip().startswith("#"):
            key, value = line.split("=", 1)
            out[key.strip()] = value.strip()
    return out


def write_env(updates: dict[str, str]) -> None:
    env = read_env()
    env.update(updates)
    body = "".join(f"{key}={value}\n" for key, value in sorted(env.items()))
    ENV_FILE.write_text(body, encoding="utf-8")


def token(kind: str) -> str | None:
    return os.environ.get(ENV_KEYS[kind]) or read_env().get(ENV_KEYS[kind]) or None


def call(method: str, tok: str, params: dict | None = None) -> dict:
    data = urllib.parse.urlencode(params or {}).encode()
    req = urllib.request.Request(f"https://slack.com/api/{method}", data=data,
                                 headers={"Authorization": f"Bearer {tok}"})
    with urllib.request.urlopen(req, timeout=30) as resp:
        body = json.load(resp)
    if not body.get("ok"):
        detail = json.dumps(body.get("errors") or body.get("response_metadata") or {})[:400]
        raise BootstrapError(f"{method} failed: {body.get('error')} {detail}")
    return body


def manifest() -> dict:
    return yaml.safe_load(MANIFEST.read_text(encoding="utf-8"))


def need(kind: str) -> str:
    tok = token(kind)
    if not tok:
        raise BootstrapError(f"missing {ENV_KEYS[kind]} (set it in the environment or .env)")
    return tok


def cmd_validate() -> None:
    call("apps.manifest.validate", need("config"), {"manifest": json.dumps(manifest())})
    print("manifest valid")


def cmd_apply() -> None:
    cfg, man = need("config"), manifest()
    call("apps.manifest.validate", cfg, {"manifest": json.dumps(man)})
    app_id = os.environ.get("SLACK_APP_ID") or read_env().get("SLACK_APP_ID")
    if app_id:
        call("apps.manifest.update", cfg, {"app_id": app_id, "manifest": json.dumps(man)})
        print(f"updated app {app_id}")
    else:
        created = call("apps.manifest.create", cfg, {"manifest": json.dumps(man)})
        app_id = created["app_id"]
        write_env({"SLACK_APP_ID": app_id})
        print(f"created app {app_id}")
    exported = call("apps.manifest.export", cfg, {"app_id": app_id})["manifest"]
    diffs = compare(man, exported)
    print("export matches manifest" if not diffs else f"export differs: {diffs}")
    print(f"install: https://api.slack.com/apps/{app_id}/install-on-team")


def compare(want: dict, got: dict) -> list[str]:
    checks = {
        "name": (want["display_information"]["name"], got["display_information"]["name"]),
        "bot": (want["features"]["bot_user"]["display_name"], got["features"]["bot_user"]["display_name"]),
        "scopes": (sorted(want["oauth_config"]["scopes"]["bot"]), sorted(got["oauth_config"]["scopes"].get("bot", []))),
        "socket_mode": (want["settings"]["socket_mode_enabled"], got["settings"].get("socket_mode_enabled")),
        "interactivity": (want["settings"]["interactivity"]["is_enabled"],
                          got["settings"].get("interactivity", {}).get("is_enabled")),
        "bot_events": (sorted(want["settings"].get("event_subscriptions", {}).get("bot_events", [])),
                       sorted(got["settings"].get("event_subscriptions", {}).get("bot_events", []))),
    }
    return [key for key, (a, b) in checks.items() if a != b]


def cmd_status() -> None:
    for kind in ("config", "bot", "app"):
        print(f"{ENV_KEYS[kind]}: {'present' if token(kind) else 'missing'}")
    env = read_env()
    for key in ("SLACK_APP_ID", "SLACK_CHANNEL_ID"):
        print(f"{key}: {env.get(key) or 'missing'}")


def cmd_channel() -> None:
    bot = need("bot")

    # Check if a stored channel ID exists; if so, handle legacy renaming
    stored_id = os.environ.get("SLACK_CHANNEL_ID") or read_env().get("SLACK_CHANNEL_ID")
    if stored_id:
        info = call("conversations.info", bot, {"channel": stored_id})
        channel_name = info["channel"]["name"]
        if channel_name in LEGACY_CHANNEL_NAMES:
            # Rename the legacy channel to the new name
            call("conversations.rename", bot, {"channel": stored_id, "name": CHANNEL_NAME})
            print(f"renamed #{channel_name} -> #{CHANNEL_NAME} {stored_id}")
        channel_id = stored_id
    else:
        # Resolve or create the channel
        channel_id, cursor = None, ""
        while channel_id is None:
            page = call("conversations.list", bot, {"types": "public_channel", "limit": 200,
                                                    "exclude_archived": "true", "cursor": cursor})
            channel_id = next((c["id"] for c in page["channels"] if c["name"] == CHANNEL_NAME), None)
            cursor = page.get("response_metadata", {}).get("next_cursor", "")
            if not cursor:
                break
        if channel_id is None:
            channel_id = call("conversations.create", bot, {"name": CHANNEL_NAME})["channel"]["id"]
            print(f"created #{CHANNEL_NAME} {channel_id}")

    call("conversations.join", bot, {"channel": channel_id})
    write_env({"SLACK_CHANNEL_ID": channel_id})
    print(f"#{CHANNEL_NAME} = {channel_id}; bot joined")


def cmd_smoke() -> None:
    import websocket  # websocket-client

    app, bot = need("app"), need("bot")
    channel = os.environ.get("SLACK_CHANNEL_ID") or read_env().get("SLACK_CHANNEL_ID")
    if not channel:
        raise BootstrapError("missing SLACK_CHANNEL_ID; run `channel` first")
    url = call("apps.connections.open", app)["url"]
    ws = websocket.create_connection(url, timeout=20)
    try:
        hello = json.loads(ws.recv())
        if hello.get("type") != "hello":
            raise BootstrapError(f"socket mode: expected hello, got {hello.get('type')}")
        print("socket mode: connected (hello received)")
    finally:
        ws.close()
    posted = call("chat.postMessage", bot, {"channel": channel, "text": "Cliff bootstrap: posting…"})
    try:
        call("chat.update", bot, {"channel": channel, "ts": posted["ts"],
                                  "text": "Cliff bootstrap: post + in-place update OK"})
        print(f"post + update OK (ts {posted['ts']})")
    finally:  # the test message is never left in the channel, even when the update fails
        call("chat.delete", bot, {"channel": channel, "ts": posted["ts"]})
        print("test message deleted")


def channel_id() -> str:
    channel = os.environ.get("SLACK_CHANNEL_ID") or read_env().get("SLACK_CHANNEL_ID")
    if not channel:
        raise BootstrapError("missing SLACK_CHANNEL_ID; run `channel` first")
    return channel


def _print_messages(msgs: list[dict]) -> None:
    for m in reversed(msgs):
        who = m.get("user") or ("Cliff" if m.get("bot_id") else "?")
        text = " ".join((m.get("text") or "").split())
        print(f"[{m.get('ts')}] {who}: {text[:300]}")


def cmd_read(limit: str = "20") -> None:
    """Last N messages in #gtm-ai-demo (channels:history). Authors are Slack user ids: no users:read scope."""
    _print_messages(call("conversations.history", need("bot"), {"channel": channel_id(), "limit": int(limit)})["messages"])


COMMANDS = {"validate": cmd_validate, "apply": cmd_apply, "status": cmd_status,
            "channel": cmd_channel, "smoke": cmd_smoke, "read": cmd_read}


def main(argv: list[str]) -> int:
    if len(argv) < 2 or argv[1] not in COMMANDS:
        print(__doc__)
        return 2
    try:
        COMMANDS[argv[1]](*argv[2:])
    except BootstrapError as exc:
        print(f"error: {exc}", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv))
