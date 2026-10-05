"""Slack smoke test for the HAR-129 demo flow of Cliff, the Slack face of Ghost (HAR-137 section 12): the
deterministic fixture first.

The Go Slack surface runs against `fakecore` (the contract fixture episode, record-only executor), so nothing
here needs the real backend. Two modes share the same assertions (`demosmoke verify`):

  python scripts/slack/demo_smoke.py [--no-human]
      The default, and the CI mode. `--no-human` is accepted and changes nothing. A fake Slack and the same Slack surface code are driven through the same interactions a
      person would click. No Slack, no credentials. It runs the flow twice, answering Message 3 once with
      Edit interpretation (a correction plus a note) and once with Don't learn this (an explicit no_learning
      verdict), each in its own run directory, and passes only when both pass every check.

  python scripts/slack/demo_smoke.py --real-core
      The same no-human CI run, but against the real core API on an embedded Postgres: the send goes
      through the actual send-time re-evaluation and the HumanDelta write is asserted (HAR-139).

  python scripts/slack/demo_smoke.py --live --i-accept-this-posts-fixture-messages-into-ghost-demo
      Live mode, in the real workspace. REFUSED unless the scary override flag is also given, because it posts
      the synthetic fixture episode ("Acme (synthetic fixture)") into the channel in SLACK_CHANNEL_ID, which is
      #ghost-demo, the audience's channel. Internal tooling must never post there (HAR-129 demo boundary). It posts
      Message 1 and Message 2, then WAITS while a person clicks:  View full -> Select B -> Edit (submit) -> Send ->
      (Message 3 posts by itself) -> Edit interpretation (submit), or Don't learn this, or Correct.  Then asserts.

Tokens come from scripts/slack/bootstrap.py's token() helper (the environment or the git-ignored .env) and go to the child processes' environment only; they are never printed or written. The
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
  2. click  Select B  on the second option (the message updates in place)
  3. click  Edit , change the subject or body, Submit
  4. click  Send  (Message 3 appears by itself after about a second)
  5. Message 3: click  Edit interpretation , change the interpretation, Submit (or Correct, or Don't learn this)
The test ends by itself when the verdict is saved (timeout: {minutes} minutes).
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


M3_ANSWERS = ("edit-interpretation", "no-learning", "correct")  # how the no-human driver answers Message 3


def run_no_human(out: Path) -> int:
    """The fixture flow once per Message 3 answer; the first run uses `out`, the others a subdirectory each."""
    bins = build(out)
    code = 0
    for answer in M3_ANSWERS:
        run_dir = out if answer == M3_ANSWERS[0] else out / answer
        run_dir.mkdir(parents=True, exist_ok=True)
        subprocess.run([str(bins["demosmoke"]), "no-human", "--out", str(run_dir), "--m3", answer], cwd=ROOT, check=True)
        print(f"-- Message 3 answered with {answer}")
        code = verify(bins, run_dir, partial=False, env=dict(os.environ)) or code
    return code


def run_real_core(out: Path) -> int:
    """CI mode against the real backend (HAR-139): embedded Postgres, the real core API and the same driver."""
    bins = build(out)
    subprocess.run([str(bins["demosmoke"]), "real-core", "--out", str(out)], cwd=ROOT, check=True)
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
        sys.exit(f"missing {', '.join(missing)} (checked the environment and .env; "
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


OVERRIDE_FLAG = "--i-accept-this-posts-fixture-messages-into-ghost-demo"


def parse_args(argv: list[str] | None = None) -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("--no-human", action="store_true",
                        help="drive the interactions through a fake Slack (CI mode; this is the default)")
    parser.add_argument("--real-core", action="store_true",
                        help="like --no-human but against the real core API on embedded Postgres (HAR-139)")
    parser.add_argument("--live", action="store_true",
                        help="post into the real Slack channel in SLACK_CHANNEL_ID (#ghost-demo); needs " + OVERRIDE_FLAG)
    parser.add_argument(OVERRIDE_FLAG, dest="allow_ghost_demo", action="store_true",
                        help="with --live: allow posting the fixture episode into #ghost-demo, the audience channel")
    parser.add_argument("--out", type=Path, help="directory for the run's logs (default: a fresh temp directory)")
    parser.add_argument("--timeout-min", type=int, default=20, help="live mode: minutes to wait for the human")
    return parser.parse_args(argv)


def mode(args: argparse.Namespace) -> str:
    """live only on an explicit --live; otherwise the fake-Slack run, against the real core when asked."""
    if args.live:
        return "live"
    return "real-core" if args.real_core else "no-human"


def guard_live(args: argparse.Namespace) -> None:
    """Live mode posts into #ghost-demo (SLACK_CHANNEL_ID): refuse unless the override is given. Runs before any
    credential is read or any Slack call is made."""
    if args.live and not args.allow_ghost_demo:
        sys.exit("refused: --live posts the synthetic fixture episode into the channel in SLACK_CHANNEL_ID, which is "
                 "#ghost-demo, the audience's channel. Internal tooling must never post there (HAR-129 demo boundary). "
                 f"Run without --live for the CI check, or add {OVERRIDE_FLAG} if you really mean it.")


def main(argv: list[str] | None = None) -> int:
    args = parse_args(argv)
    guard_live(args)
    out = args.out or Path(tempfile.mkdtemp(prefix="ghost-demo-smoke-"))
    out.mkdir(parents=True, exist_ok=True)
    print(f"run directory: {out}")
    chosen = mode(args)
    if chosen == "live":
        code = run_live(out, args.timeout_min)
    elif chosen == "real-core":
        code = run_real_core(out)
    else:
        code = run_no_human(out)
    print("SMOKE PASSED" if code == 0 else "SMOKE FAILED")
    return code


if __name__ == "__main__":
    sys.exit(main())
