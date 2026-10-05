"""The codespace's devcontainer: its contract with the repository, checked without Docker.

CI additionally builds the image and runs scripts/codespace/check-toolchain.sh inside it (the `devcontainer` job).
Nothing here reads docs/ or any Markdown: the public CI mirror strips them.
"""
from __future__ import annotations

import json
import re
import shutil
import subprocess
from pathlib import Path

import pytest

ROOT = Path(__file__).resolve().parents[2]
CONFIG = json.loads((ROOT / ".devcontainer" / "devcontainer.json").read_text(encoding="utf-8"))
SCRIPTS = ROOT / "scripts" / "codespace"
SECRET_NAMES = {"OPENROUTER_API_KEY", "SLACK_BOT_TOKEN", "SLACK_APP_TOKEN", "SLACK_CHANNEL_ID"}


def feature(name: str) -> dict:
    return CONFIG["features"][f"ghcr.io/devcontainers/features/{name}:1"]


def bash_usable() -> bool:
    if shutil.which("bash") is None:
        return False
    try:
        return subprocess.run(["bash", "-c", "true"], capture_output=True, check=False, timeout=20).returncode == 0
    except (OSError, subprocess.SubprocessError):
        return False


def memory_gb(value: str) -> int:
    return int(re.fullmatch(r"(\d+)gb", value).group(1))


class TestMachine:
    def test_asks_for_four_cores_and_sixteen_gigabytes(self):
        host = CONFIG["hostRequirements"]
        assert host["cpus"] >= 4
        assert memory_gb(host["memory"]) >= 16
        assert memory_gb(host["storage"]) >= 32

    def test_base_image_is_a_pinned_ubuntu_not_latest(self):
        assert re.search(r":ubuntu-\d\d\.\d\d$", CONFIG["image"])


class TestToolchain:
    def test_go_matches_go_mod_exactly(self):
        go_mod = (ROOT / "core-go" / "go.mod").read_text(encoding="utf-8")
        wanted = re.search(r"(?m)^go (\S+)$", go_mod).group(1)
        assert feature("go")["version"] == wanted

    def test_python_is_3_12(self):
        assert feature("python")["version"].startswith("3.12")
        pyproject = (ROOT / "worker-py" / "pyproject.toml").read_text(encoding="utf-8")
        assert 'requires-python = ">=3.12"' in pyproject

    def test_node_satisfies_the_web_engines_range(self):
        engines = json.loads((ROOT / "web" / "package.json").read_text(encoding="utf-8"))["engines"]["node"]
        assert engines.startswith(">=22")
        assert int(feature("node")["version"].split(".")[0]) >= 22

    def test_java_is_a_version_the_pinned_neo4j_runs_on(self):
        assert feature("java")["version"] in {"17", "21"}

    def test_the_github_cli_is_there_for_the_data_upload_fallback(self):
        assert "ghcr.io/devcontainers/features/github-cli:1" in CONFIG["features"]


class TestPortsAndSecrets:
    def test_only_the_web_port_is_forwarded_and_labelled(self):
        assert CONFIG["forwardPorts"] == [3000]
        assert CONFIG["portsAttributes"]["3000"]["label"] == "Ghost demo (web)"
        assert CONFIG["portsAttributes"]["3000"]["onAutoForward"] == "openBrowser"

    def test_every_internal_service_port_of_the_runner_is_never_forwarded(self):
        layout = (ROOT / "core-go" / "internal" / "demorun" / "layout.go").read_text(encoding="utf-8")
        runner_ports = {int(p) for p in re.findall(r'pick\("GHOST_DEMO_PORT_\w+", (\d+)\)', layout)}
        control = int(re.search(r"DefaultControlPort = (\d+)", (ROOT / "core-go" / "internal" / "codespace" / "assemble.go").read_text(encoding="utf-8")).group(1))
        internal = (runner_ports | {control}) - {3000}
        assert internal == {8080, 8090, 15432, 17687, 8099}, "a runner port changed: update devcontainer.json"
        for port in internal:
            assert CONFIG["portsAttributes"][str(port)]["onAutoForward"] == "ignore", port
        assert CONFIG["otherPortsAttributes"]["onAutoForward"] == "ignore"

    def test_the_four_secrets_are_declared_without_values(self):
        assert set(CONFIG["secrets"]) == SECRET_NAMES
        for name, spec in CONFIG["secrets"].items():
            assert set(spec) <= {"description", "documentationUrl"}, name

    def test_no_secret_value_or_credential_shape_is_in_the_config_or_scripts(self):
        pattern = re.compile(r"sk-or-[A-Za-z0-9]|xox[bpe]-[0-9A-Za-z]|xapp-[0-9]")
        for path in [ROOT / ".devcontainer" / "devcontainer.json", *SCRIPTS.glob("*")]:
            if path.is_file() and path.suffix in {".json", ".sh", ".py"}:
                assert not pattern.search(path.read_text(encoding="utf-8")), path.name

    def test_runtime_model_is_the_qwen_flash_model(self):
        assert CONFIG["containerEnv"]["GHOST_MODEL"] == "openrouter/qwen/qwen3.8-flash"

    def test_no_container_env_carries_a_secret(self):
        assert not (set(CONFIG["containerEnv"]) & SECRET_NAMES)


class TestLifecycle:
    def test_create_and_start_run_scripts_that_exist(self):
        for key in ("postCreateCommand", "postStartCommand"):
            script = CONFIG[key].removeprefix("bash ")
            assert (ROOT / script).is_file(), f"{key} runs {script}"

    def test_start_returns_immediately_and_runs_one_boot_at_a_time(self):
        start = (SCRIPTS / "post-start.sh").read_text(encoding="utf-8")
        assert "nohup" in start and "flock -n" in start and "&\n" in start
        assert "codespace up" in start

    def test_setup_is_ordered_and_fails_loudly_when_the_data_does_not_verify(self):
        create = (SCRIPTS / "post-create.sh").read_text(encoding="utf-8")
        assert "set -euo pipefail" in create
        order = [create.index(s) for s in ("simple_salesforce", "npm ci", "go mod download", "fetch_crmarena.py", "ghostctl.sh codespace setup")]
        assert order == sorted(order)
        assert "gh codespace cp" in create
        assert "--data data/crmarena_b2b" in create

    @pytest.mark.skipif(not bash_usable(), reason="a working bash is not available (on Windows `bash` may be the WSL stub)")
    @pytest.mark.parametrize("name", sorted(p.name for p in SCRIPTS.glob("*.sh")))
    def test_scripts_are_valid_bash(self, name):
        done = subprocess.run(["bash", "-n", str(SCRIPTS / name)], capture_output=True, text=True, check=False)
        assert done.returncode == 0, done.stderr


class TestDataIsNeverCommitted:
    def test_the_dataset_and_the_demo_state_are_git_ignored(self):
        ignored = (ROOT / ".gitignore").read_text(encoding="utf-8").splitlines()
        assert "/data/" in ignored
        assert ".demo/" in ignored

    @pytest.mark.skipif(shutil.which("git") is None or not (ROOT / ".git").exists(), reason="not a git checkout")
    def test_no_crmarena_record_file_is_tracked(self):
        tracked = subprocess.run(["git", "-C", str(ROOT), "ls-files"], capture_output=True, text=True, check=True).stdout.splitlines()
        # The dataset lives in the top-level data/ (and the demo state in .demo/); a directory named like the dataset
        # anywhere else is a copy of it. bench/data/ is code, and the pinned manifest is a file (metadata, no records).
        offenders = [f for f in tracked
                     if Path(f).parts[0] in {"data", ".demo"} or {"crmarena_b2b", "CRMArenaPro"} & set(Path(f).parts[:-1])]
        assert offenders == []
