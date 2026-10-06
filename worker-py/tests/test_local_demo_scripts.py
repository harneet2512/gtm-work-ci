"""scripts/demo: the laptop demo's PowerShell (setup, Start/Stop shortcuts, autostart). Static checks, nothing is run."""
from __future__ import annotations

import os
import re
import shutil
import subprocess
from pathlib import Path

import pytest

ROOT = Path(__file__).resolve().parents[2]
DEMO = ROOT / "scripts" / "demo"
RM_NO_SLACK = "Remove-Item Env:" + chr(92) + "GHOST_DEMO_NO_SLACK"
NEW = ["local-common.ps1", "record-local.ps1", "setup-local.ps1", "start-demo.ps1", "stop-demo.ps1", "reset-demo.ps1", "create-shortcuts.ps1", "autostart.ps1"]


def text(name: str) -> str:
    return (DEMO / name).read_text(encoding="utf-8")


@pytest.mark.skipif(shutil.which("powershell") is None, reason="Windows PowerShell is not available")
@pytest.mark.parametrize("name", NEW)
def test_scripts_parse(name, tmp_path):
    probe = tmp_path / "parse.ps1"
    probe.write_text(
        "$e=$null; [void][System.Management.Automation.Language.Parser]::ParseFile($args[0],[ref]$null,[ref]$e);"
        " if($e.Count){ $e | % { Write-Output $_.Message }; exit 1 }", encoding="utf-8")
    done = subprocess.run(["powershell", "-NoProfile", "-ExecutionPolicy", "Bypass", "-File", str(probe), str(DEMO / name)],
                          capture_output=True, text=True, check=False)
    assert done.returncode == 0, done.stdout + done.stderr


def test_everything_lives_under_the_demo_home_which_defaults_to_the_d_drive():
    common = text("local-common.ps1")
    assert "GHOST_DEMO_HOME" in common and "D:\\ghost-demo" in common
    assert "GHOST_DEMO_HOME" in text("setup-local.ps1")


def test_shortcuts_run_hidden_powershell_and_are_named_start_stop_and_reset():
    shortcuts = text("create-shortcuts.ps1")
    assert "-WindowStyle Hidden" in shortcuts
    for script in ("start-demo.ps1", "stop-demo.ps1", "reset-demo.ps1"):
        assert script in shortcuts, script
    assert "Start" in shortcuts and "Stop" in shortcuts and "Reset" in shortcuts and "0x2013" in shortcuts
    # three shortcuts and no more: no Continue or Switch shortcut either (Play continues into the later episode)
    assert shortcuts.count("New-DemoShortcut \"") == 3


def test_reset_forces_a_restore_whatever_ghost_demo_start_says():
    start, reset = text("start-demo.ps1"), text("reset-demo.ps1")
    assert "-Mode restore" in reset
    assert "param([string]$Mode" in start and "'--mode', $Mode" in start


def test_start_resets_the_demo_and_the_reset_shortcut_is_the_same_hidden_path():
    start, reset = text("start-demo.ps1"), text("reset-demo.ps1")
    assert "resets" in start.lower() and "codespace', 'up'" in start
    assert "start-demo.ps1" in reset and "Write-DemoLog" in reset
    assert "-WindowStyle Hidden" in text("create-shortcuts.ps1")
    assert "/admin" not in reset and "cloud_access" not in reset


def test_start_opens_the_browser_only_through_the_runner_and_never_prints_secrets():
    start = text("start-demo.ps1")
    assert "codespace', 'up'" in start and "Start-Process $url" in start
    assert "GhostDemoStart" in start  # one start at a time
    for name in NEW:
        body = text(name)
        assert "cloud_access" not in body, name
        assert not re.search(r"sk-or-|xox[bpe]-|xapp-", body), name
        assert not re.search(r"Write-(Host|Output)[^\n]*\$env:(OPENROUTER|SLACK)", body), name


def test_stop_stops_everything_through_the_runner():
    assert "codespace', 'down'" in text("stop-demo.ps1")


def test_setup_copies_the_three_data_sets_verifies_the_hash_and_is_idempotent():
    setup = text("setup-local.ps1")
    for needle in ("crmarena_b2b", "crmarena_extraction", "CRMArenaPro", "fetch_crmarena.py", "codespace setup",
                   "Copy-Once", "-CheckOnly", "create-shortcuts.ps1"):
        assert needle in setup, needle
    assert "if (Test-Path $To)" in setup  # an existing copy is left alone


def test_autostart_is_an_optional_per_user_logon_task():
    auto = text("autostart.ps1")
    assert "-AtLogOn" in auto and "-Enable" in auto and "-Disable" in auto and "-WindowStyle Hidden" in auto


def run_ps(snippet: str) -> str:
    """Dot-sources local-common.ps1 and runs a snippet (pure helpers only: nothing is built or started)."""
    script = f". '{DEMO / 'local-common.ps1'}'; {snippet}"
    done = subprocess.run(["powershell", "-NoProfile", "-ExecutionPolicy", "Bypass", "-Command", script],
                          capture_output=True, text=True, check=False, env={**os.environ, "GHOST_DEMO_HOME": str(ROOT / "nowhere")})
    assert done.returncode == 0, done.stdout + done.stderr
    return done.stdout.strip()


@pytest.mark.skipif(shutil.which("powershell") is None, reason="Windows PowerShell is not available")
def test_the_browser_opens_the_control_plane_on_the_active_frozen_case_in_demo_mode():
    status = ("'{\"cases\":[{\"slot\":\"case1\",\"active\":false,\"manifest_id\":\"m1\"},"
              "{\"slot\":\"case2\",\"active\":true,\"manifest_id\":\"m2\"}]}' | ConvertFrom-Json")
    assert run_ps(f"Get-DemoUrl ({status}) 3000") == "http://127.0.0.1:3000/control?manifest=m2&demo=1"


@pytest.mark.skipif(shutil.which("powershell") is None, reason="Windows PowerShell is not available")
def test_without_an_active_frozen_case_the_browser_opens_the_system_page_and_honours_the_web_port():
    empty = "'{\"cases\":[{\"slot\":\"case1\",\"active\":true}]}' | ConvertFrom-Json"
    assert run_ps(f"Get-DemoUrl ({empty}) 4100") == "http://127.0.0.1:4100/system"


def test_no_script_sends_the_audience_to_an_admin_page_or_names_the_removed_page():
    for name in NEW:
        assert "/admin" not in text(name), name
    assert "/control?manifest=" in text("local-common.ps1") and "demo=1" in text("local-common.ps1")


def test_the_record_run_turns_slack_off_and_always_restores_the_environment():
    record = text("record-local.ps1")
    assert "$env:GHOST_DEMO_NO_SLACK = '1'" in record
    assert record.count(RM_NO_SLACK) >= 1 and "finally" in record
    # both passes (recording and the strict verification) go through the one Slack-off function
    assert record.count("Invoke-RecordPass $exe") == 2 and "-Strict" in record
    # no other call starts the demo outside that function
    outside = record.split("function Invoke-RecordPass", 1)[1].split("$exe = Build-Ghostctl", 1)[1]
    assert "'codespace', 'up'" not in outside


def test_the_user_start_always_runs_with_slack_on():
    start = text("start-demo.ps1")
    assert RM_NO_SLACK in start
    assert start.index(RM_NO_SLACK) < start.index("'codespace', 'up'")


def test_the_record_run_resumes_and_documents_the_spend_guard_and_no_slack_posts():
    record = text("record-local.ps1")
    assert "7.50" in record and "run this script again to resume" in record
    assert "nothing is posted" in record and "#ghost-demo" not in record.replace("demo channel", "")


def test_the_second_case_is_ecolite_innovations_everywhere_in_the_scripts():
    for name in NEW:
        assert "EcoVision" not in text(name), name
    assert "EcoLite Innovations" in text("setup-local.ps1")


def test_no_demo_code_reads_a_credential_file():
    """Secrets come only from the environment and .env: nothing the demo runs may name the credential file."""
    roots = [ROOT / "scripts" / "demo", ROOT / "scripts" / "codespace", ROOT / "core-go" / "internal" / "demorun",
             ROOT / "core-go" / "internal" / "codespace", ROOT / "core-go" / "cmd" / "ghostctl", ROOT / "scripts" / "slack"]
    offenders = []
    for root in roots:
        for path in root.rglob("*"):
            if path.is_file() and path.suffix in {".go", ".ps1", ".sh", ".py"} and "cloud_access" in path.read_text(encoding="utf-8").lower():
                if path.name not in ("test_local_demo_scripts.py",):
                    offenders.append(str(path.relative_to(ROOT)))
    assert not offenders, offenders
    assert not (ROOT / "scripts" / "codespace" / "set_secrets.py").exists()
    assert not (ROOT / "scripts" / "codespace" / "set-secrets.sh").exists()


def test_the_record_run_plays_the_slack_path_and_writes_the_hidden_run_sheet():
    record = text("record-local.ps1")
    assert "REAL Slack handlers" in record and "RUNSHEET.txt" in record and "SAME path" in record
