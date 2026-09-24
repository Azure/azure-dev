# Copyright (c) Microsoft Corporation. All rights reserved.
# Licensed under the MIT License.

import json
from pathlib import Path
import subprocess
import tempfile
import unittest
from unittest import mock

import scenario


class EnvironmentGateTests(unittest.TestCase):
    def context(self, root):
        plan = root / "plan.json"
        plan.write_text("{}")
        return {
            "SERVICE_PLAN": str(plan), "AZD_SCENARIO_LIVE_ENVIRONMENT": "approved-ci",
            "GITHUB_REPOSITORY": "Azure/azure-dev", "GITHUB_SERVER_URL": "https://github.com",
            "GITHUB_OUTPUT": str(root / "outputs"), "GH_TOKEN": "mock-read-only-token",
        }

    def test_only_confirmed_existing_protected_environment_is_forwarded(self):
        with tempfile.TemporaryDirectory() as root:
            root = Path(root)
            env = self.context(root)
            result = subprocess.CompletedProcess([], 0, '{"name":"approved-ci","requiredReviewers":1}', "")
            with mock.patch.object(scenario.subprocess, "run", return_value=result) as run:
                self.assertEqual(scenario.github_live_gate(root / "evidence", env), 0)
            self.assertEqual((root / "outputs").read_text(), "environment_name=approved-ci\n")
            argv = run.call_args.args[0]
            self.assertEqual(argv[argv.index("--method") + 1], "GET")
            self.assertIn("repos/Azure/azure-dev/environments/approved-ci", argv)
            report = json.loads((root / "evidence" / "environment-gate.json").read_text())
            self.assertEqual(report["status"], "PASS")
            self.assertEqual(report["execution"], "NOT RUN")
            self.assertIn("native approval is still required", report["reason"])
            self.assertNotIn("mock-read-only-token", json.dumps(report))

    def test_missing_plan_environment_or_token_blocks_without_api(self):
        for key in ("SERVICE_PLAN", "AZD_SCENARIO_LIVE_ENVIRONMENT", "GH_TOKEN", "GITHUB_OUTPUT"):
            with self.subTest(key=key), tempfile.TemporaryDirectory() as root:
                root = Path(root)
                env = self.context(root)
                env[key] = ""
                with mock.patch.object(scenario.subprocess, "run") as run:
                    self.assertEqual(scenario.github_live_gate(root / "evidence", env), 3)
                    run.assert_not_called()
                report = json.loads((root / "evidence" / "environment-gate.json").read_text())
                self.assertEqual(report["status"], "BLOCKED")

    def test_missing_denied_or_unprotected_environment_is_never_bound_or_created(self):
        cases = [
            subprocess.CompletedProcess([], 1, "", "HTTP 404"),
            subprocess.CompletedProcess([], 1, "", "HTTP 403"),
            subprocess.CompletedProcess([], 0, '{"name":"approved-ci","requiredReviewers":0}', ""),
            subprocess.CompletedProcess([], 0, '{"name":"approved-ci","requiredReviewers":true}', ""),
            subprocess.CompletedProcess([], 0, '{"name":"different","requiredReviewers":1}', ""),
            subprocess.CompletedProcess([], 0, "not-json", ""),
        ]
        for result in cases:
            with self.subTest(result=result), tempfile.TemporaryDirectory() as root:
                root = Path(root)
                env = self.context(root)
                with mock.patch.object(scenario.subprocess, "run", return_value=result) as run:
                    self.assertEqual(scenario.github_live_gate(root / "evidence", env), 3)
                    run.assert_called_once()
                    argv = run.call_args.args[0]
                    self.assertEqual(argv[argv.index("--method") + 1], "GET")
                self.assertFalse((root / "outputs").exists())
                report = json.loads((root / "evidence" / "environment-gate.json").read_text())
                self.assertEqual(report["status"], "BLOCKED")
                self.assertEqual(report["execution"], "NOT RUN")


if __name__ == "__main__":
    unittest.main()
