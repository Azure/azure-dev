# Copyright (c) Microsoft Corporation. All rights reserved.
# Licensed under the MIT License.

from contextlib import redirect_stderr
import io
import json
from pathlib import Path
import tempfile
import unittest
from unittest import mock

import service
import test_agent_cli
import test_owned_prompt
import test_service


class ServiceErrorPrivacyTests(unittest.TestCase):
    def test_plan_config_executable_and_runtime_os_errors_never_publish_absolute_paths(self):
        for boundary in ("plan", "config", "executable", "runtime"):
            for filename in (r"C:\sentinel-private-root\secret-input.json",
                             "/sentinel-private-root/secret-input.json"):
                with self.subTest(boundary=boundary, filename=filename), tempfile.TemporaryDirectory() as root:
                    root = Path(root)
                    alias_parent = root / "alias-parent"
                    alias_parent.mkdir()
                    profile = alias_parent / ".." / "profile"
                    profile.mkdir()
                    plan, _ = test_service.ServiceTests().installed_fixture(profile)
                    path = root / "plan.json"
                    raw = json.dumps(plan).encode()
                    path.write_bytes(raw)
                    output = root / "evidence"
                    env = {**test_service.ServiceTests().github_env(),
                           "AZD_SCENARIO_LIVE_APPROVAL_SHA256": service.scenario.sha256(raw),
                           "AZD_SCENARIO_LIVE_AUTH_CONFIG": str(profile)}
                    failure = PermissionError(13, "synthetic read failure", filename)
                    read_bytes, read_text = Path.read_bytes, Path.read_text
                    executable_path = Path(plan["azdExecutable"]).resolve()
                    config_path = (profile / "config.json").resolve()
                    injected = []

                    def read_file_bytes(file):
                        target = path if boundary == "plan" else executable_path
                        if boundary in ("plan", "executable") and file == target:
                            injected.append(boundary)
                            raise failure
                        return read_bytes(file)

                    def read_file_text(file, *args, **kwargs):
                        if boundary == "config" and file == config_path:
                            injected.append(boundary)
                            raise failure
                        return read_text(file, *args, **kwargs)

                    stderr = io.StringIO()
                    with mock.patch.dict(service.os.environ, env), \
                         mock.patch.object(Path, "read_bytes", read_file_bytes), \
                         mock.patch.object(Path, "read_text", read_file_text), \
                         mock.patch.object(service, "lifecycle", side_effect=failure) as lifecycle, \
                         mock.patch.object(service, "run_owned_process") as run, \
                         mock.patch.object(service.sys, "argv", [
                             "service.py", "--plan", str(path), "--output", str(output)]), redirect_stderr(stderr):
                        self.assertEqual(service.main(), 1 if boundary == "runtime" else 3)
                        run.assert_not_called()
                        if boundary != "runtime":
                            lifecycle.assert_not_called()
                            self.assertEqual(injected, [boundary], "The intended preflight read must hit the error seam")
                    receipt = json.loads((output / "service-status.json").read_bytes())
                    combined = json.dumps(receipt) + stderr.getvalue()
                    self.assertNotIn("sentinel-private-root", combined)
                    self.assertNotIn("secret-input.json", combined)
                    self.assertIn("PermissionError", combined)
                    self.assertEqual(receipt["status"], "FAIL" if boundary == "runtime" else "BLOCKED")
                    self.assertEqual(receipt["execution"], "STARTED" if boundary == "runtime" else "NOT RUN")

    def test_primary_and_resource_cleanup_os_errors_omit_paths_from_nested_receipts(self):
        labels = (
            "verify existing service identity",
            "delete only owned evaluation and runs",
            "delete only owned dataset version",
            "delete only owned invocation session",
            "clear owned local agent state",
        )
        for label in labels:
            with self.subTest(label=label), tempfile.TemporaryDirectory() as root:
                root = Path(root)
                driver = test_agent_cli.FakeCliDriver(self, root)
                record = driver.record

                def fail_operation(name, kwargs):
                    record(name, kwargs)
                    if name == label:
                        raise FileNotFoundError(2, "synthetic missing file",
                                                r"C:\sentinel-private-root\secret-config.json")

                driver.record = fail_operation
                report = {}
                with self.assertRaises((OSError, RuntimeError)):
                    service.existing_agent_cli_lifecycle(
                        driver.plan, driver, root, report, test_owned_prompt.ROW)
                self.assertNotIn("sentinel-private-root", json.dumps(report))
                self.assertNotIn("secret-config.json", json.dumps(report))
                self.assertIn("FileNotFoundError", json.dumps(report))

    def test_prompt_cleanup_os_error_omits_the_external_path(self):
        with tempfile.TemporaryDirectory() as root:
            root = Path(root)
            plan = test_owned_prompt.plan_for()
            driver = test_owned_prompt.FakeOwnedDriver(self, plan, root)
            record = driver.record

            def fail_agent_cleanup(label, detail, kwargs):
                record(label, detail, kwargs)
                if label == "delete only owned prompt-agent version":
                    raise PermissionError(13, "synthetic token config error",
                                          "/sentinel-private-root/secret-token-config.json")

            driver.record = fail_agent_cleanup
            report = {}
            with self.assertRaises(RuntimeError):
                service.owned_prompt_lifecycle(plan, driver, root, report, test_owned_prompt.ROW)
            self.assertEqual(report["agentCleanup"]["status"], "FAIL")
            self.assertNotIn("sentinel-private-root", json.dumps(report))
            self.assertIn("PermissionError", report["agentCleanup"]["message"])

    def test_local_cleanup_os_error_does_not_leak_into_the_final_receipt_or_stderr(self):
        with tempfile.TemporaryDirectory() as root:
            root = Path(root)
            profile = root / "profile"
            profile.mkdir()
            plan, _ = test_service.ServiceTests().installed_fixture(profile)
            path = root / "plan.json"
            raw = json.dumps(plan).encode()
            path.write_bytes(raw)
            env = {**test_service.ServiceTests().github_env(),
                   "AZD_SCENARIO_LIVE_APPROVAL_SHA256": service.scenario.sha256(raw),
                   "AZD_SCENARIO_LIVE_AUTH_CONFIG": str(profile)}
            cleanup = service.scenario.proof_module.cleanup_owned_workspace

            def cleanup_failure(workspace):
                cleanup(workspace)
                raise PermissionError(13, "synthetic cleanup failure",
                                      r"C:\sentinel-private-root\private-workspace")

            stderr = io.StringIO()
            with mock.patch.dict(service.os.environ, env), \
                 mock.patch.object(service, "lifecycle"), \
                 mock.patch.object(service.scenario.proof_module, "cleanup_owned_workspace", cleanup_failure), \
                 mock.patch.object(service.sys, "argv", [
                     "service.py", "--plan", str(path), "--output", str(root / "evidence")]), redirect_stderr(stderr):
                self.assertEqual(service.main(), 1)
            report = json.loads((root / "evidence" / "service-status.json").read_bytes())
            self.assertEqual(report["cleanup"]["status"], "FAIL")
            self.assertNotIn("sentinel-private-root", json.dumps(report) + stderr.getvalue())
            self.assertIn("PermissionError", report["cleanup"]["error"])


if __name__ == "__main__":
    unittest.main()
