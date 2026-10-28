import json
import os
import shutil
import subprocess
import tempfile
import unittest
from pathlib import Path

from test_sanitize_results import completed_summary, exported_items

SCRIPT_ROOT = Path(__file__).resolve().parent

MOCK_COMMANDS = r"""
$ErrorActionPreference = "Stop"
function az {
    $global:LASTEXITCODE = 0
    "offline-unit-test-subscription"
}
function azd {
    ConvertTo-Json -InputObject @($args) -Compress |
        Add-Content -LiteralPath (Join-Path $env:TEST_DIRECTORY "commands.jsonl")
    $global:LASTEXITCODE = 0
    $command = $args -join " "
    switch -Wildcard ($command) {
        "version" { "azd version offline-unit-test" }
        "extension install *" {
            if ($env:TEST_SCENARIO -eq "install-failed") { $global:LASTEXITCODE = 1 }
        }
        "extension list *" {
            $source = if ($env:TEST_SCENARIO -eq "wrong-source") { "other" } else { "azd" }
            $version = if ($env:TEST_SCENARIO -eq "old-package") { "1.0.0-beta.1" } else { "1.0.0-beta.2" }
            $packages = @(
                @{id="azure.ai.evaluations";source=$source;version="1.0.0-beta.2";installedVersion=$version},
                @{id="azure.ai.dataset";source=$source;version="1.0.0-beta.2";installedVersion=$version}
            )
            if ($env:TEST_SCENARIO -eq "missing-package") { $packages = @($packages[0]) }
            ConvertTo-Json -InputObject $packages
        }
        "ai eval version *" {
            if ($env:TEST_SCENARIO -eq "route-failed") { $global:LASTEXITCODE = 1 }
        }
        "ai dataset version *" {}
        "config set *" {}
        "up *" {
            if ($env:TEST_SCENARIO -eq "up-failed") { $global:LASTEXITCODE = 1 }
        }
        "ai eval run start *" {
            if ($env:TEST_SCENARIO -eq "invalid-start") { "{service-secret" }
            else { '{"run_id":"offline-test-run"}' }
        }
        "ai eval run show *" {
            Get-Content -LiteralPath (Join-Path $env:TEST_DIRECTORY "summary.json") -Raw
            if ($env:TEST_SCENARIO -in @(
                "breach", "breach-item-passed", "pass-breach-exit", "result-error", "result-skipped",
                "result-sample-error", "result-label-skipped"
            )) { $global:LASTEXITCODE = 2 }
            if ($env:TEST_SCENARIO -in @("exit-mismatch", "breach-operational-failure")) {
                $global:LASTEXITCODE = 1
            }
            if ($env:TEST_SCENARIO -eq "breach-unexpected-exit") { $global:LASTEXITCODE = 17 }
        }
        "ai eval run output export *" {
            $outputIndex = [Array]::IndexOf($args, "--output-file")
            Copy-Item -LiteralPath (Join-Path $env:TEST_DIRECTORY "export.json") -Destination $args[$outputIndex + 1]
        }
        default { throw "Unexpected mocked command: $command" }
    }
}
& $env:TEST_SCRIPT @TestArguments
exit $LASTEXITCODE
"""


class PowerShellScriptTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls) -> None:
        cls.pwsh = shutil.which("pwsh")
        if cls.pwsh is None:
            raise RuntimeError("PowerShell 7 is required to test the CI scripts.")

    def run_script(self, root: Path, script: str, scenario: str = "passed") -> subprocess.CompletedProcess:
        environment = {
            **os.environ,
            "TEST_SCRIPT": str(SCRIPT_ROOT / script),
            "TEST_DIRECTORY": str(root),
            "TEST_SCENARIO": scenario,
            "AZD_CONFIG_DIR": str(root / "azd-config"),
            "AZURE_AI_PROJECT_ENDPOINT": "https://offline.services.ai.azure.com/api/projects/offline",
            "AZURE_LOCATION": "offline",
            "AZURE_RESOURCE_GROUP": "offline",
            "NO_COLOR": "1",
            "AZURE_DEV_COLLECT_TELEMETRY": "no",
        }
        arguments = (
            '$TestArguments = @{TemporaryDirectory=$env:TEST_DIRECTORY;'
            'EvidenceDirectory=(Join-Path $env:TEST_DIRECTORY "evidence")}\n'
            if script == "run_live.ps1" else "$TestArguments = @{}\n"
        )
        harness = root / "harness.ps1"
        harness.write_text(arguments + MOCK_COMMANDS, encoding="utf-8")
        return subprocess.run(
            [self.pwsh, "-NoLogo", "-NoProfile", "-NonInteractive", "-File", str(harness)],
            env=environment, capture_output=True, text=True, check=False, timeout=30,
        )

    def test_shared_installer_requests_latest_canonical_packages_and_starts_both_routes(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            result = self.run_script(root, "install_packages.ps1")
            self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
            commands = [
                json.loads(line) for line in (root / "commands.jsonl").read_text(encoding="utf-8").splitlines()
            ]
            installs = [command for command in commands if command[:2] == ["extension", "install"]]
            self.assertEqual(
                installs,
                [["extension", "install", extension, "--source", "azd", "--no-prompt"]
                 for extension in ("azure.ai.evaluations", "azure.ai.dataset")],
            )
            self.assertIn(["extension", "list", "--installed", "--source", "azd", "--output", "json"], commands)
            self.assertIn(["ai", "eval", "version", "--output", "json"], commands)
            self.assertIn(["ai", "dataset", "version", "--output", "json"], commands)

    def test_shared_installer_fails_on_install_registry_version_or_route_errors(self) -> None:
        for scenario in ("install-failed", "wrong-source", "old-package", "missing-package", "route-failed"):
            with self.subTest(scenario=scenario), tempfile.TemporaryDirectory() as directory:
                result = self.run_script(Path(directory), "install_packages.ps1", scenario)
                self.assertNotEqual(result.returncode, 0, result.stdout + result.stderr)

    def test_live_runner_requires_exit_zero_for_pass_and_two_for_breach(self) -> None:
        for scenario in (
            "passed", "breach", "exit-mismatch", "pass-breach-exit",
            "breach-operational-failure", "breach-zero-exit", "breach-unexpected-exit",
            "result-error", "result-skipped", "result-sample-error", "result-label-skipped",
            "item-error", "item-skipped", "item-failed", "breach-item-passed",
            "pass-label-fail", "pass-label-failed",
            "invalid-start", "up-failed",
        ):
            with self.subTest(scenario=scenario), tempfile.TemporaryDirectory() as directory:
                root = Path(directory)
                breached = scenario.startswith(("breach", "result-"))
                (root / "summary.json").write_text(
                    json.dumps(completed_summary(passed=1 if breached else 2, failed=1 if breached else 0)),
                    encoding="utf-8",
                )
                export = exported_items([1.0, 0.0 if breached else 1.0])
                result_metadata = {
                    "result-error": {"status": "errored"},
                    "result-skipped": {"status": "skipped"},
                    "result-sample-error": {"sample": {"error": {"code": "FAILED_EXECUTION"}}},
                    "result-label-skipped": {"label": "skipped"},
                    "pass-label-fail": {"label": "fail"},
                    "pass-label-failed": {"label": "failed"},
                }
                export["items"][1]["results"][0].update(result_metadata.get(scenario, {}))
                item_statuses = {
                    "item-error": "errored", "item-skipped": "skipped",
                    "item-failed": "failed", "breach-item-passed": "passed",
                }
                if scenario in item_statuses:
                    export["items"][1]["status"] = item_statuses[scenario]
                (root / "export.json").write_text(json.dumps(export), encoding="utf-8")
                result = self.run_script(root, "run_live.ps1", scenario)
                output = result.stdout + result.stderr
                self.assertNotIn("service-secret", output)
                self.assertNotIn("raw service explanation", output)
                self.assertFalse(list(root.glob("eval-scenario-*")), "Raw work files were not removed.")
                artifact = root / "evidence" / "results.json"
                if scenario in ("passed", "breach"):
                    self.assertEqual(result.returncode, 2 if breached else 0, output)
                    self.assertIn("variable=EvalScenarioArtifactReady]true", output)
                    safe = json.loads(artifact.read_text(encoding="utf-8"))
                    self.assertEqual(safe["outcome"], "quality_gate_breached" if breached else "passed")
                    commands = [
                        json.loads(line)
                        for line in (root / "commands.jsonl").read_text(encoding="utf-8").splitlines()
                    ]
                    lifecycle = [command for command in commands if command[0] != "config"]
                    self.assertEqual([command[:4] for command in lifecycle], [
                        ["up", "--no-prompt"],
                        ["ai", "eval", "run", "start"],
                        ["ai", "eval", "run", "show"],
                        ["ai", "eval", "run", "output"],
                    ])
                    self.assertIn("pass-rate=0.8", lifecycle[2])
                    self.assertEqual(lifecycle[2][4], "offline-test-run")
                    self.assertEqual(lifecycle[3][5], "offline-test-run")
                else:
                    self.assertEqual(result.returncode, 1, output)
                    self.assertNotIn("variable=EvalScenarioArtifactReady]true", output)
                    self.assertFalse(artifact.exists())


if __name__ == "__main__":
    unittest.main()
