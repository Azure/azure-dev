# Copyright (c) Microsoft Corporation. All rights reserved.
# Licensed under the MIT License.

from datetime import datetime, timedelta, timezone
import base64
import json
import io
from pathlib import Path
import subprocess
import tempfile
import unittest
from unittest import mock

import service


ROW = b'{"query":"two plus two","response":"4","ground_truth":"4"}\n'


class ServiceTests(unittest.TestCase):
    def identity(self, plan):
        claims = json.dumps({"appid": plan["clientId"], "tid": plan["tenantId"]}).encode()
        return {"token": "e30." + base64.urlsafe_b64encode(claims).decode().rstrip("=") + ".mock"}

    def plan(self):
        return {
            "schemaVersion": 1, "mode": "static-evaluation", "provider": "github",
            "workflowCommit": "a" * 40, "runId": "42",
            "expiresAt": (datetime.now(timezone.utc) + timedelta(hours=1)).isoformat(),
            "authorizedOperations": ["dataset-download", "eval-create", "eval-run", "eval-export", "eval-delete"],
            "approvalReference": "test-only-approval", "resourceOwner": "test-only-owner",
            "budgetControlReference": "test-only-external-control",
            "budgetControlExternallyVerified": True, "approvedBudget": "1",
            "maxRuns": 1, "maxRows": 1, "timeoutSeconds": 30, "durationSeconds": 120,
            "clientId": "00000000-0000-0000-0000-000000000001",
            "tenantId": "00000000-0000-0000-0000-000000000002",
            "datasetName": "fixture", "datasetVersion": "7", "datasetSha256": service.scenario.sha256(ROW),
            "evaluator": "builtin.task_adherence", "judgeModel": "fixture-judge", "ownerPrefix": "ci-fixture",
            "projectEndpoint": "https://fixture.services.ai.azure.com/api/projects/fixture",
            "azdExecutable": "not-executed-in-mock-tests",
            "binarySha256": {"azd": "a" * 64, "azure.ai.evaluations": "b" * 64, "azure.ai.dataset": "c" * 64},
            "versions": {"azure.ai.evaluations": "1.0.42-beta", "azure.ai.dataset": "1.0.0-beta.30"},
        }

    def test_default_gate_calls_no_command_and_saves_blocked_reason(self):
        with tempfile.TemporaryDirectory() as root:
            root = Path(root)
            plan = root / "plan.json"
            plan.write_text(json.dumps(self.plan()))
            with mock.patch.object(service, "Driver") as driver:
                with self.assertRaises(service.Blocked):
                    service.execute(plan, root / "evidence", env={})
                driver.assert_not_called()
            report = json.loads((root / "evidence" / "service-status.json").read_text())
            self.assertEqual(report["status"], "BLOCKED")
            self.assertEqual(report["execution"], "NOT RUN")
            self.assertIn("approval digest", report["error"]["message"])

    def test_gate_binds_approval_provider_run_revision_and_bounds(self):
        plan = self.plan()
        env = {"AZD_SCENARIO_LIVE_APPROVAL_SHA256": "b" * 64,
               "GITHUB_REPOSITORY": "fixture/repo", "GITHUB_RUN_ID": "42", "GITHUB_SHA": "a" * 40}
        service.validate_plan(plan, "b" * 64, env)
        for key, value in (("schemaVersion", True), ("runId", "wrong"), ("workflowCommit", "c" * 40),
                           ("budgetControlExternallyVerified", False), ("approvedBudget", "NaN"),
                           ("maxRuns", 2), ("maxRows", True), ("mode", "agent-deploy"),
                           ("expiresAt", "2000-01-01T00:00:00Z"),
                           ("projectEndpoint", "https://user:secret@fixture.services.ai.azure.com/api/projects/x")):
            with self.subTest(key=key):
                invalid = {**plan, key: value}
                with self.assertRaises(service.Blocked):
                    service.validate_plan(invalid, "b" * 64, env)
        with self.assertRaises(service.Blocked):
            service.validate_plan(plan, "different-digest", env)
        with self.assertRaises(service.Blocked):
            service.validate_plan({**plan, "deploy": True}, "b" * 64, env)

    def test_user_identity_is_rejected_before_service_commands(self):
        calls = []
        with tempfile.TemporaryDirectory() as root:
            def driver(label, args, **kwargs):
                calls.append(label)
                return {"status": "authenticated", "type": "user", "clientId": self.plan()["clientId"]}
            with self.assertRaisesRegex(service.Blocked, "service principal"):
                service.lifecycle(self.plan(), driver, Path(root), {})
        self.assertEqual(calls, ["verify existing service identity"])

    def test_approved_binary_mismatch_is_rejected_before_execution(self):
        plan = self.plan()
        with tempfile.TemporaryDirectory() as root:
            executable = Path(root) / "azd"
            executable.write_bytes(b"wrong bytes")
            plan["azdExecutable"] = str(executable)
            with self.assertRaisesRegex(service.Blocked, "Core executable"):
                service.verify_install(plan, Path(root))

    def installed_fixture(self, root):
        plan = self.plan()
        core = root / "azd"
        core.write_bytes(b"approved core")
        plan["azdExecutable"] = str(core)
        plan["binarySha256"]["azd"] = service.scenario.sha256(core.read_bytes())
        installed = {}
        for extension, command in service.scenario.EXTENSIONS.items():
            platform = "windows-amd64" if service.os.name == "nt" else "linux-amd64"
            entry = extension.replace(".", "-") + "-" + platform + (".exe" if service.os.name == "nt" else "")
            binary = root / "extensions" / extension / entry
            binary.parent.mkdir(parents=True)
            binary.write_bytes(extension.encode())
            plan["binarySha256"][extension] = service.scenario.sha256(binary.read_bytes())
            installed[extension] = {
                "id": extension, "namespace": "ai." + command, "version": plan["versions"][extension],
                "path": str(binary.relative_to(root)),
            }
        settings = {"extension": {"installed": installed}}
        (root / "config.json").write_text(json.dumps(settings))
        return plan, settings

    def test_install_verifies_the_persisted_execution_paths(self):
        with tempfile.TemporaryDirectory() as root:
            root = Path(root)
            plan, _ = self.installed_fixture(root)
            self.assertEqual(service.verify_install(plan, root), (root / "azd").resolve())

    def test_redirected_metadata_cannot_hide_behind_an_approved_conventional_binary(self):
        with tempfile.TemporaryDirectory() as root:
            root = Path(root)
            plan, settings = self.installed_fixture(root)
            redirected = root / "other-binary"
            redirected.write_bytes(b"unapproved binary, even if it reports an approved version")
            settings["extension"]["installed"]["azure.ai.evaluations"]["path"] = str(redirected.relative_to(root))
            (root / "config.json").write_text(json.dumps(settings))
            with self.assertRaisesRegex(service.Blocked, "execution path"):
                service.verify_install(plan, root)

    def test_install_rejects_unapproved_routes_and_escaping_paths(self):
        for field, value in (("namespace", "ai.other"), ("version", "not-approved"),
                             ("path", "../outside"), ("path", "C:\\outside.exe"),
                             ("path", "/outside")):
            with self.subTest(field=field), tempfile.TemporaryDirectory() as root:
                root = Path(root)
                plan, settings = self.installed_fixture(root)
                settings["extension"]["installed"]["azure.ai.evaluations"][field] = value
                (root / "config.json").write_text(json.dumps(settings))
                with self.assertRaises(service.Blocked):
                    service.verify_install(plan, root)
        with tempfile.TemporaryDirectory() as root:
            root = Path(root)
            plan, settings = self.installed_fixture(root)
            settings["extension"]["installed"]["another-extension"] = {"namespace": "ai.eval"}
            (root / "config.json").write_text(json.dumps(settings))
            with self.assertRaisesRegex(service.Blocked, "exactly the two"):
                service.verify_install(plan, root)

    def drive(self, *, failure=None, bad_rows=False, cleanup_fails=False, counts=None):
        plan = self.plan()
        calls, report = [], {}
        with tempfile.TemporaryDirectory() as root:
            workspace = Path(root)

            def driver(label, args, **kwargs):
                calls.append((label, args))
                if label == failure or (cleanup_fails and label.startswith("delete only")):
                    raise RuntimeError(label + " failed")
                if label.startswith("verify existing"):
                    return {"status": "authenticated", "type": "servicePrincipal", "clientId": plan["clientId"]}
                if label == "verify native service token identity":
                    self.assertTrue(kwargs["private_output"])
                    return self.identity(plan)
                if label.startswith("verify approved"):
                    extension = "azure.ai.evaluations" if args[1] == "eval" else "azure.ai.dataset"
                    return {"name": extension, "version": plan["versions"][extension]}
                if label.startswith("download"):
                    Path(args[args.index("--output-file") + 1]).write_bytes(ROW * (2 if bad_rows else 1))
                    return None
                if label.startswith("create"):
                    config = json.loads((workspace / "azure.eval.yaml").read_text())
                    self.assertNotIn("file", config["datasets"][0])
                    self.assertEqual(config["datasets"][0]["version"], "7")
                    self.assertNotIn("target", config["evals"][0])
                    self.assertNotIn("simulation", config["evals"][0])
                    return {"name": "ci-fixture-unique", "id": "eval_owned"}
                if label.startswith("start"):
                    self.assertIn("--no-wait", args)
                    self.assertNotIn("--max-samples", args)
                    return {"eval_id": "eval_owned", "run_id": "evalrun_owned"}
                if label.startswith("wait"):
                    return {"id": "evalrun_owned", "status": "completed",
                            "result_counts": {"total": 1, "passed": 1} if counts is None else counts}
                if label.startswith("export"):
                    return {"run": {"id": "evalrun_owned"}, "items": [{"private": "not persisted"}]}
                if label.startswith("delete"):
                    self.assertEqual(args, ["ID-only DELETE", "eval_owned"])
                    return {"id": "eval_owned", "status": "deleted"}
                self.fail("Unexpected command")

            driver.delete_owned_eval = lambda eval_id, endpoint, tenant: driver(
                "delete only owned evaluation and runs", ["ID-only DELETE", eval_id])
            try:
                service.lifecycle(plan, driver, workspace, report, name="ci-fixture-unique")
            except (service.Blocked, RuntimeError) as error:
                report["testException"] = str(error)
        return calls, report

    def test_mocked_sequence_uses_only_owned_eval_and_exact_existing_dataset(self):
        calls, report = self.drive()
        self.assertEqual(len(calls), 10)
        self.assertEqual(report["quality"], "PASS")
        self.assertEqual(report["remoteCleanup"]["status"], "PASS")
        self.assertTrue(report["remoteBillingStopped"].startswith("NOT VERIFIED"))
        text = json.dumps(report)
        self.assertNotIn("not persisted", text)
        self.assertFalse(any("agent" in args or "generate" in args for _, args in calls))

    def test_unapproved_dataset_bytes_stop_before_create(self):
        calls, report = self.drive(bad_rows=True)
        self.assertEqual(len(calls), 5)
        self.assertIn("dataset bytes differ", report["testException"])

    def test_ambiguous_create_is_not_retried_or_deleted_by_guessed_name(self):
        calls, report = self.drive(failure="create owned static evaluation")
        self.assertEqual(len(calls), 6)
        self.assertTrue(report["remoteCleanup"]["manualReconciliationRequired"])
        self.assertEqual(report["remoteCleanup"]["status"], "BLOCKED")

    def test_native_identity_token_must_match_client_and_tenant(self):
        plan = self.plan()
        service.verify_native_identity_token(self.identity(plan), plan)
        wrong = {**plan, "tenantId": "00000000-0000-0000-0000-000000000003"}
        with self.assertRaisesRegex(service.Blocked, "client/tenant"):
            service.verify_native_identity_token(self.identity(wrong), plan)

    def test_run_failure_still_deletes_only_owned_eval(self):
        calls, report = self.drive(failure="start one authorized run")
        self.assertEqual(calls[-1][0], "delete only owned evaluation and runs")
        self.assertEqual(report["remoteCleanup"]["status"], "PASS")
        self.assertNotIn("quality", report)

    def test_primary_and_remote_cleanup_failures_are_both_retained(self):
        calls, report = self.drive(failure="wait for owned run", cleanup_fails=True)
        self.assertIn("wait for owned run", report["failure"]["message"])
        self.assertEqual(report["remoteCleanup"]["status"], "FAIL")
        self.assertNotIn("quality", report)

    def test_boolean_or_coerced_quality_counts_never_pass(self):
        for counts in ({"total": True, "passed": True, "failed": False},
                       {"total": 1, "passed": 1, "errored": False},
                       {"total": "1", "passed": 1},
                       {"total": 1, "passed": service.Decimal("1.0")}):
            with self.subTest(counts=counts):
                _, report = self.drive(counts=counts)
                self.assertNotIn("quality", report)
                self.assertIn("JSON integers", report["failure"]["message"])
                self.assertEqual(report["remoteCleanup"]["status"], "PASS")

    def test_real_driver_keeps_service_payload_out_of_receipt(self):
        with tempfile.TemporaryDirectory() as root:
            workspace = Path(root)
            report = {}
            driver = service.Driver(Path("azd"), workspace / "auth", workspace, 17, 60, report)
            output = subprocess.CompletedProcess([], 0, b'{"id":"eval_owned","private":"do-not-publish"}', b"")
            with mock.patch.object(service.subprocess, "run", return_value=output) as execute:
                result = driver("fixture read", ["ai", "eval", "show", "eval_owned"])
            self.assertEqual(result["id"], "eval_owned")
            self.assertNotIn("do-not-publish", json.dumps(report))
            self.assertIn("--no-prompt", execute.call_args.args[0])
            self.assertEqual(execute.call_args.kwargs["timeout"], 17)
            for secret in ("AZURE_CLIENT_SECRET", "GITHUB_TOKEN", "SYSTEM_ACCESSTOKEN"):
                self.assertNotIn(secret, execute.call_args.kwargs["env"])

    def test_dataset_identity_is_used_but_not_published(self):
        with tempfile.TemporaryDirectory() as root:
            report = {}
            driver = service.Driver(Path("azd"), Path(root) / "auth", Path(root), 17, 60, report)
            completed = subprocess.CompletedProcess([], 0, b"{}", b"")
            with mock.patch.object(service.subprocess, "run", return_value=completed) as run:
                driver("download approved registered version", [
                    "ai", "dataset", "download", "private-existing-dataset",
                    "--version", "private-version-42", "--output-file", str(Path(root) / "row.jsonl"),
                ])
            self.assertIn("private-existing-dataset", run.call_args.args[0])
            self.assertIn("private-version-42", run.call_args.args[0])
            public = json.dumps(report)
            self.assertNotIn("private-existing-dataset", public)
            self.assertNotIn("private-version-42", public)
            self.assertIn("<approved-dataset>", public)
            self.assertIn("<approved-dataset-version>", public)

    def test_observation_deadline_does_not_prevent_owned_cleanup(self):
        with tempfile.TemporaryDirectory() as root:
            report = {}
            driver = service.Driver(Path("azd"), Path(root) / "auth", Path(root), 17, 60, report)
            driver.deadline = 0
            result = subprocess.CompletedProcess([], 0, b'{"token":"mock-credential-do-not-log"}', b"")
            response = mock.MagicMock()
            response.__enter__.return_value.status = 204
            opener = mock.Mock()
            opener.open.return_value = response
            with mock.patch.object(service.subprocess, "run", return_value=result) as run, \
                 mock.patch.object(service.urllib.request, "build_opener", return_value=opener):
                with self.assertRaisesRegex(RuntimeError, "deadline"):
                    driver("wait for owned run", [])
                run.assert_not_called()
                driver.delete_owned_eval("eval_owned",
                                         "https://private.services.ai.azure.com/api/projects/private",
                                         self.plan()["tenantId"])
                self.assertLessEqual(run.call_args.kwargs["timeout"], 17)
                self.assertGreater(run.call_args.kwargs["timeout"], 0)
                self.assertNotIn("private.services", json.dumps(report))
                self.assertNotIn("mock-credential", json.dumps(report))

    def test_id_only_delete_never_falls_back_on_404_or_follows_redirects(self):
        with tempfile.TemporaryDirectory() as root:
            driver = service.Driver(Path("azd"), Path(root) / "auth", Path(root), 17, 60, {})
            token = subprocess.CompletedProcess([], 0, b'{"token":"mock-credential"}', b"")
            opener = mock.Mock()
            endpoint = self.plan()["projectEndpoint"]
            url = endpoint + "/openai/v1/evals/eval_owned"
            opener.open.side_effect = service.urllib.error.HTTPError(url, 404, "gone", {}, io.BytesIO())
            with mock.patch.object(service.subprocess, "run", return_value=token), \
                 mock.patch.object(service.urllib.request, "build_opener", return_value=opener) as build:
                self.assertEqual(driver.delete_owned_eval("eval_owned", endpoint, self.plan()["tenantId"]),
                                 {"id": "eval_owned", "status": "deleted"})
                opener.open.assert_called_once()
                request = opener.open.call_args.args[0]
                self.assertEqual(request.full_url, url)
                self.assertEqual(request.method, "DELETE")
                self.assertEqual(request.get_header("Authorization"), "Bearer mock-credential")
                redirect = build.call_args.args[0]
                self.assertIsNone(redirect.redirect_request(request, None, 302, "", {}, "https://elsewhere.invalid"))

    def test_execute_persists_primary_and_remote_cleanup_failures(self):
        plan = self.plan()
        with tempfile.TemporaryDirectory() as root:
            root = Path(root)
            plan_path = root / "plan.json"
            plan_path.write_text(json.dumps(plan))
            config = root / "auth"
            config.mkdir()
            output = root / "evidence"

            def fail_lifecycle(plan, driver, workspace, report):
                report["failure"] = {"type": "RuntimeError", "message": "primary service failure"}
                report["remoteCleanup"] = {"status": "FAIL", "message": "remote cleanup failure"}
                raise RuntimeError("remote cleanup failure")

            with mock.patch.object(service, "validate_plan", return_value={}), \
                 mock.patch.object(service, "verify_install", return_value=Path("azd")), \
                 mock.patch.object(service, "lifecycle", side_effect=fail_lifecycle):
                with self.assertRaises(RuntimeError):
                    service.execute(plan_path, output, env={"AZD_SCENARIO_LIVE_AUTH_CONFIG": str(config)})
            report = json.loads((output / "service-status.json").read_text())
            self.assertEqual(report["status"], "FAIL")
            self.assertEqual(report["failure"]["message"], "primary service failure")
            self.assertEqual(report["remoteCleanup"]["message"], "remote cleanup failure")
            self.assertEqual(report["cleanup"]["status"], "PASS")

    def test_runtime_cleanup_block_is_a_failed_execution_not_a_prerequisite_block(self):
        plan = self.plan()
        with tempfile.TemporaryDirectory() as root:
            root = Path(root)
            plan_path = root / "plan.json"
            plan_path.write_text(json.dumps(plan))
            config = root / "auth"
            config.mkdir()
            output = root / "evidence"

            def failed_cleanup(plan, driver, workspace, report):
                report["remoteCleanup"] = {"status": "FAIL", "message": "cleanup token unavailable"}
                raise service.Blocked("cleanup token unavailable")

            with mock.patch.object(service, "validate_plan", return_value={}), \
                 mock.patch.object(service, "verify_install", return_value=Path("azd")), \
                 mock.patch.object(service, "lifecycle", side_effect=failed_cleanup):
                with self.assertRaises(RuntimeError) as raised:
                    service.execute(plan_path, output, env={"AZD_SCENARIO_LIVE_AUTH_CONFIG": str(config)})
                self.assertNotIsInstance(raised.exception, service.Blocked)
            report = json.loads((output / "service-status.json").read_text())
            self.assertEqual(report["status"], "FAIL")
            self.assertEqual(report["execution"], "STARTED")
            self.assertEqual(report["remoteCleanup"]["status"], "FAIL")


if __name__ == "__main__":
    unittest.main()
