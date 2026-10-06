"""Guards for the Slack developer scripts (HAR-129 demo boundary): internal tooling never posts into #ghost-demo.

Run:  python scripts/slack/test_slack_scripts.py
No Slack, no network, no credentials: every Slack call is replaced.
"""
from __future__ import annotations

import importlib.util
import sys
import unittest
from pathlib import Path
from unittest import mock

HERE = Path(__file__).resolve().parent


def load(name: str):
    spec = importlib.util.spec_from_file_location(name, HERE / f"{name}.py")
    module = importlib.util.module_from_spec(spec)
    sys.modules[name] = module
    spec.loader.exec_module(module)
    return module


smoke = load("demo_smoke")
bootstrap = load("bootstrap")


class DemoSmokeGuards(unittest.TestCase):
    def test_no_human_is_the_default_mode(self):
        args = smoke.parse_args([])
        self.assertFalse(args.live)
        self.assertEqual(smoke.mode(args), "no-human")

    def test_real_core_is_still_selectable(self):
        self.assertEqual(smoke.mode(smoke.parse_args(["--real-core"])), "real-core")

    def test_live_is_refused_without_the_override(self):
        with self.assertRaises(SystemExit) as raised:
            smoke.guard_live(smoke.parse_args(["--live"]))
        self.assertIn("#ghost-demo", str(raised.exception))
        self.assertIn("SLACK_CHANNEL_ID", str(raised.exception))

    def test_live_runs_only_with_the_scary_override(self):
        args = smoke.parse_args(["--live", smoke.OVERRIDE_FLAG])
        smoke.guard_live(args)  # does not exit
        self.assertEqual(smoke.mode(args), "live")

    def test_the_override_alone_does_not_start_live_mode(self):
        self.assertEqual(smoke.mode(smoke.parse_args([smoke.OVERRIDE_FLAG])), "no-human")

    def test_main_never_reads_credentials_by_default(self):
        with mock.patch.object(smoke, "credentials", side_effect=AssertionError("read credentials")), \
                mock.patch.object(smoke, "run_no_human", return_value=0) as run:
            self.assertEqual(smoke.main([]), 0)
        run.assert_called_once()

    def test_main_refuses_live_before_touching_credentials_or_slack(self):
        with mock.patch.object(smoke, "credentials", side_effect=AssertionError("read credentials")), \
                mock.patch.object(smoke, "run_live", side_effect=AssertionError("went live")):
            with self.assertRaises(SystemExit):
                smoke.main(["--live"])


class BootstrapGuards(unittest.TestCase):
    def test_tokens_come_only_from_the_environment_and_dot_env(self):
        # a credential file with valid-looking tokens, in the places the old code looked, changes nothing
        import tempfile
        with tempfile.TemporaryDirectory() as tmp:
            cred = Path(tmp) / ("cloud_" + "access.md")
            cred.write_text("bot xoxb-1111-2222-fake app xapp-1-A0-3333-fake", encoding="utf-8")
            with mock.patch.dict(bootstrap.os.environ, {"GHOST_SLACK_CRED_FILE": str(cred), "HOME": tmp, "USERPROFILE": tmp}, clear=False),                     mock.patch.object(bootstrap, "read_env", return_value={}):
                for name in ("SLACK_BOT_TOKEN", "SLACK_APP_TOKEN", "SLACK_CONFIG_TOKEN"):
                    bootstrap.os.environ.pop(name, None)
                self.assertIsNone(bootstrap.token("bot"))
                self.assertIsNone(bootstrap.token("app"))
                with self.assertRaises(bootstrap.BootstrapError) as err:
                    bootstrap.need("bot")
                self.assertIn("SLACK_BOT_TOKEN", str(err.exception))
                self.assertNotIn("credential file", str(err.exception))

    def test_env_wins_and_dot_env_is_the_fallback(self):
        with mock.patch.dict(bootstrap.os.environ, {"SLACK_BOT_TOKEN": "xoxb-env"}, clear=False),                 mock.patch.object(bootstrap, "read_env", return_value={"SLACK_BOT_TOKEN": "xoxb-dot", "SLACK_APP_TOKEN": "xapp-dot"}):
            self.assertEqual(bootstrap.token("bot"), "xoxb-env")
            bootstrap.os.environ.pop("SLACK_APP_TOKEN", None)
            self.assertEqual(bootstrap.token("app"), "xapp-dot")

    def test_no_script_names_the_credential_file(self):
        for name in ("bootstrap.py", "demo_smoke.py"):
            self.assertNotIn("cloud_" + "access", (HERE / name).read_text(encoding="utf-8"), name)
            self.assertNotIn("GHOST_SLACK_CRED_FILE", (HERE / name).read_text(encoding="utf-8"), name)

    def test_say_and_dms_are_gone(self):
        self.assertNotIn("say", bootstrap.COMMANDS)
        self.assertNotIn("dms", bootstrap.COMMANDS)
        self.assertNotIn("say TEXT", bootstrap.__doc__)

    def test_smoke_deletes_its_own_message(self):
        calls: list[tuple[str, dict]] = []

        def fake_call(method, tok, params=None):
            calls.append((method, dict(params or {})))
            return {"url": "wss://example.invalid", "ts": "1.0"}

        socket = mock.Mock()
        socket.recv.return_value = '{"type": "hello"}'
        fake_ws = mock.Mock(create_connection=mock.Mock(return_value=socket))
        with mock.patch.dict(sys.modules, {"websocket": fake_ws}), \
                mock.patch.object(bootstrap, "call", fake_call), \
                mock.patch.object(bootstrap, "need", return_value="tok"), \
                mock.patch.dict("os.environ", {"SLACK_CHANNEL_ID": "CTEST"}):
            bootstrap.cmd_smoke()
        methods = [m for m, _ in calls]
        self.assertEqual(methods[-1], "chat.delete")
        self.assertEqual(calls[-1][1], {"channel": "CTEST", "ts": "1.0"})
        self.assertLess(methods.index("chat.postMessage"), methods.index("chat.delete"))

    def test_smoke_deletes_even_when_the_update_fails(self):
        calls: list[str] = []

        def fake_call(method, tok, params=None):
            calls.append(method)
            if method == "chat.update":
                raise bootstrap.BootstrapError("update failed")
            return {"url": "wss://example.invalid", "ts": "1.0"}

        socket = mock.Mock()
        socket.recv.return_value = '{"type": "hello"}'
        fake_ws = mock.Mock(create_connection=mock.Mock(return_value=socket))
        with mock.patch.dict(sys.modules, {"websocket": fake_ws}), \
                mock.patch.object(bootstrap, "call", fake_call), \
                mock.patch.object(bootstrap, "need", return_value="tok"), \
                mock.patch.dict("os.environ", {"SLACK_CHANNEL_ID": "CTEST"}):
            with self.assertRaises(bootstrap.BootstrapError):
                bootstrap.cmd_smoke()
        self.assertEqual(calls[-1], "chat.delete")


if __name__ == "__main__":
    unittest.main()
