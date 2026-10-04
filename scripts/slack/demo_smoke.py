"""Slack smoke test for the HAR-129 demo flow (HAR-137 section 12): the deterministic fixture first.

The Go Slack surface runs against `fakecore` (the contract fixture episode, record-only executor), so nothing
here needs the real backend. Two modes share the same assertions (`demosmoke verify`):

  python scripts/slack/demo_smoke.py --no-human
      CI mode. A fake Slack and the same Slack surface code are driven through the same interactions a
      person would click. No Slack, no credentials.

  python scripts/slack/demo_smoke.py
      Live mode, in the real workspace (#ghost-demo). Posts Message 1 and Message 2, then WAITS while a
      person clicks:  View full -> Choose B -> Edit (submit) -> Send -> (Message 3 posts by itself) ->
      Needs correction (submit).  Then asserts.

Tokens come from scripts/slack/bootstrap.py's token() helper (environment, git-ignored .env or the local
credential file) and go to the child processes' environment only; they are never printed or written. The
assertions count messages from the bot's own Slack writes (GHOST_SLACK_AUDIT_FILE), so no channels:history
scope is needed. The report ends with the exit code: 0 only when every check passes, and a scan proves no
token reached any log file of the run.
"""
from __future__ import annotations

import argparse
import importlib.util
import json
import os
import re
import secrets
import subprocess
import sys
import tempfile
import time
import urllib.request
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
CORE = ROOT / "core-go"
EXE = ".exe" if os.name == "nt" else ""
# The fixture episode served by fakecore (core-go/internal/slacksurface/fixture.go).
RUN_ID = "0f0a0000-0000-4000-8000-000000000601"
ACCOUNT_ID = "0a0c0000-0000-4000-8000-000000000001"
SECRET_ENV = "SLACK_APP_TOKEN,SLACK_BOT_TOKEN,GHOST_API_TOKEN"
GUIDE = """
Message 1 and Message 2 are in the channel. In Slack, in this order:
  1. Message 2: click  View full  on any strategy (a modal opens; close it)
  2. click  Choose  on Strategy B (the message updates in place)
  3. click  Edit , change the subject or body, Submit
  4. click  Send  (Message 3 appears by itself after about a second)
  5. Message 3: click  Needs correction , type a correction, Submit
The test ends by itself when the correction is saved (timeout: {minutes} minutes).
"""


def build(out: Path) -> dict[str, Path]:
    """Compile the three commands into out/bin, so a Windows firewall prompt or a slow `go run` cannot interfere."""
    bins = {}
    (out / "bin").mkdir(parents=True, exist_ok=True)
    for name in ("fakecore", "slackbot", "demosmoke"):
        target = out / "bin" / (name + EXE)
        subprocess.run(["go", "build", "-o", str(target), f"./cmd/{name}"], cwd=CORE, check=True)
        bins[name] = target
    return bins


def run_no_human(out: Path) -> int:
    bins = build(out)
    subprocess.run([str(bins["demosmoke"]), "no-human", "--out", str(out)], cwd=ROOT, check=True)
    return verify(bins, out, partial=False, env=dict(os.environ))


def verify(bins: dict[str, Path], out: Path, partial: bool, env: dict[str, str]) -> int:
    cmd = [str(bins["demosmoke"]), "verify", "--dir", str(out), "--secret-env", SECRET_ENV]
    if partial:
        cmd.append("--allow-partial")
    return subprocess.run(cmd, cwd=ROOT, env=env).returncode


def load_bootstrap():
    spec = importlib.util.spec_from_file_location("ghost_slack_bootstrap", ROOT / "scripts" / "slack" / "bootstrap.py")
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


def credentials() -> dict[str, str]:
    """Slack tokens and the channel id via the bootstrap helper; values are returned, never printed."""
    boot = load_bootstrap()
    found = {"SLACK_BOT_TOKEN": boot.token("bot"), "SLACK_APP_TOKEN": boot.token("app"),
             "SLACK_CHANNEL_ID": os.environ.get("SLACK_CHANNEL_ID") or boot.read_env().get("SLACK_CHANNEL_ID")}
    missing = [name for name, value in found.items() if not value]
    if missing:
        sys.exit(f"missing {', '.join(missing)} (checked the environment, .env and the credential file; "
                 f"run scripts/slack/bootstrap.py status)")
    return found


def wait_for(predicate, seconds: float, what: str, proc: subprocess.Popen | None = None):
    deadline = time.time() + seconds
    while time.time() < deadline:
        if proc is not None and proc.poll() is not None:
            sys.exit(f"{what}: the process exited early with code {proc.returncode}")
        value = predicate()
        if value:
            return value
        time.sleep(0.25)
    sys.exit(f"timed out waiting for {what}")


def read_core_log(path: Path) -> dict:
    try:
        return json.loads(path.read_text(encoding="utf-8"))
    except (OSError, ValueError):  # not written yet, or caught mid-rewrite: try again
        return {"entries": []}


def verdict_saved(path: Path) -> bool:
    return any(e["method"] == "POST" and e["path"].endswith("/judgment-verdict") and e["status"] == 200
               and "verdict" in (e.get("request") or {}) for e in read_core_log(path)["entries"])


def start_fakecore(bins: dict[str, Path], out: Path, env: dict[str, str]) -> tuple[subprocess.Popen, str]:
    err = open(out / "fakecore.log", "wb")
    proc = subprocess.Popen([str(bins["fakecore"]), "--log", str(out / "fakecore-log.json"), "--repo", str(ROOT)],
                            stdout=subprocess.PIPE, stderr=err, env=env, text=True)
    line = proc.stdout.readline()
    match = re.search(r"listening on (http://\S+)", line)
    if not match:
        proc.kill()
        sys.exit("fakecore did not start (see fakecore.log)")
    url = match.group(1)
    request = urllib.request.Request(f"{url}/runs/{RUN_ID}/strategies", headers={"Authorization": f"Bearer {env['GHOST_API_TOKEN']}"})
    urllib.request.urlopen(request, timeout=10).read()  # the fixture episode is really being served
    return proc, url


def run_live(out: Path, minutes: int) -> int:
    creds = credentials()
    bins = build(out)
    env = dict(os.environ, **creds, GHOST_API_TOKEN=secrets.token_hex(16),
               GHOST_SLACK_AUDIT_FILE=str(out / "slack-audit.jsonl"))
    if not env.get("SLACK_ALLOWED_USER_IDS"):
        env["SLACK_ALLOW_ALL_USERS"] = "1"  # fakecore only records; nothing is sent anywhere
    core, url = start_fakecore(bins, out, env)
    env["CORE_URL"] = url
    bot_log = open(out / "slackbot.log", "wb")
    bot = subprocess.Popen([str(bins["slackbot"])], cwd=ROOT, env=env, stdout=bot_log, stderr=subprocess.STDOUT)
    try:
        wait_for(lambda: b"slack socket connected" in (out / "slackbot.log").read_bytes(), 40, "the Socket Mode connection", bot)
        post_log = open(out / "slackbot-post.log", "wb")
        subprocess.run([str(bins["slackbot"]), "--post-run", RUN_ID, "--post-account", ACCOUNT_ID],
                       cwd=ROOT, env=env, stdout=post_log, stderr=subprocess.STDOUT, check=True)
        print(GUIDE.format(minutes=minutes), flush=True)
        wait_for(lambda: verdict_saved(out / "fakecore-log.json"), minutes * 60, "the human to finish the clicks", bot)
        time.sleep(3)  # let the in-place update of Message 3 reach Slack
    finally:
        for proc in (bot, core):
            proc.terminate()
        for proc in (bot, core):
            proc.wait(timeout=15)
    return verify(bins, out, partial=False, env=env)


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("--no-human", action="store_true", help="drive the interactions through a fake Slack (CI mode)")
    parser.add_argument("--out", type=Path, help="directory for the run's logs (default: a fresh temp directory)")
    parser.add_argument("--timeout-min", type=int, default=20, help="live mode: minutes to wait for the human")
    args = parser.parse_args()
    out = args.out or Path(tempfile.mkdtemp(prefix="ghost-demo-smoke-"))
    out.mkdir(parents=True, exist_ok=True)
    print(f"run directory: {out}")
    code = run_no_human(out) if args.no_human else run_live(out, args.timeout_min)
    print("SMOKE PASSED" if code == 0 else "SMOKE FAILED")
    return code


if __name__ == "__main__":
    sys.exit(main())
