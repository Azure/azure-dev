# Copyright (c) Microsoft Corporation. All rights reserved.
# Licensed under the MIT License.

import json
import base64
from pathlib import Path
import subprocess
import tempfile
import unittest
from unittest import mock

import service
import test_service


ROW = b'{"query":"Reply with the word blue.","ground_truth":"blue"}\n'


def plan_for(row_path="not-executed"):
    plan = test_service.ServiceTests().plan()
    del plan["datasetName"]
    plan.update(mode="owned-prompt-evaluation", datasetFile=str(row_path),
                datasetSha256=service.scenario.sha256(ROW), datasetVersion="7.0",
                agentModel="approved-existing-model", agentInstructions="Answer briefly without tools.")
    plan["authorizedOperations"] += [
        "agent-version-create", "agent-version-delete", "dataset-create", "dataset-delete",
    ]
    return plan


class FakeOwnedDriver:
    def __init__(self, case, plan, workspace, *, failure=None, cleanup_fails=False, agent_exists=False,
                 malformed_agent=False, wrong_row=False, bad_agent_delete=False):
        self.case, self.plan, self.workspace = case, plan, workspace
        self.failure, self.cleanup_fails = failure, cleanup_fails
        self.agent_exists, self.malformed_agent, self.wrong_row = agent_exists, malformed_agent, wrong_row
        self.bad_agent_delete = bad_agent_delete
        self.calls, self.cleanup_deadlines = [], []
        self.agent = self.dataset = None
        self.eval_id, self.run_id = "eval-owned", "evalrun-owned"

    def record(self, label, detail, kwargs):
        self.calls.append((label, detail))
        if "cleanup_deadline" in kwargs:
            self.cleanup_deadlines.append(kwargs["cleanup_deadline"])
        if label == self.failure or (self.cleanup_fails and label.startswith("delete only")):
            raise RuntimeError(label + " failed")

    def __call__(self, label, args, **kwargs):
        self.record(label, args, kwargs)
        if label == "verify existing service identity":
            return {"status": "authenticated", "type": "servicePrincipal", "clientId": self.plan["clientId"]}
        if label == "verify native service token identity":
            self.case.assertTrue(kwargs["private_output"])
            return test_service.ServiceTests().identity(self.plan)
        if label.startswith("verify approved"):
            extension = "azure.ai.evaluations" if args[1] == "eval" else "azure.ai.dataset"
            return {"name": extension, "version": self.plan["versions"][extension]}
        if label == "create owned manual dataset version":
            self.case.assertEqual(args[:3], ["ai", "dataset", "create"])
            self.case.assertEqual(Path(args[args.index("--from-file") + 1]).read_bytes(), ROW)
            self.dataset = {"name": args[3], "version": args[args.index("--version") + 1]}
            self.case.assertEqual(self.dataset["version"], "7.0")
            return self.dataset
        if label == "download approved registered version":
            self.case.assertEqual(args[3], self.dataset["name"])
            self.case.assertEqual(args[args.index("--version") + 1], self.dataset["version"])
            Path(args[args.index("--output-file") + 1]).write_bytes(ROW if not self.wrong_row else ROW + ROW)
            return None
        if label == "create owned evaluation":
            config = json.loads((self.workspace / "azure.eval.yaml").read_text())
            self.case.assertEqual(config["datasets"], [self.dataset])
            self.case.assertEqual(config["evals"][0]["target"], {"type": "agent", "name": self.agent["name"]})
            self.case.assertNotIn("simulation", config["evals"][0])
            self.case.assertNotIn("file", config["datasets"][0])
            return {"id": self.eval_id, "name": args[3]}
        if label == "start one authorized run":
            self.case.assertIn("--no-wait", args)
            return {"eval_id": self.eval_id, "run_id": self.run_id}
        if label == "wait for owned run":
            return {"id": self.run_id, "status": "completed", "result_counts": {"total": 1, "passed": 1}}
        if label == "export owned run":
            return {"run": {"id": self.run_id},
                    "items": [{"run_id": self.run_id, "datasource_item": json.loads(ROW)}]}
        if label == "delete only owned dataset version":
            self.case.assertEqual(args[:4], ["ai", "dataset", "delete", self.dataset["name"]])
            self.case.assertEqual(args[args.index("--version") + 1], self.dataset["version"])
            self.case.assertIn("--force", args)
            return {**self.dataset, "status": "deleted"}
        self.case.fail("Unexpected CLI operation: " + label)

    def request(self, label, method, endpoint, path, tenant, **kwargs):
        self.record(label, (method, path), kwargs)
        self.case.assertEqual(endpoint, self.plan["projectEndpoint"])
        self.case.assertEqual(tenant, self.plan["tenantId"])
        if label == "check new agent name is absent":
            self.case.assertEqual(method, "GET")
            return (200, {"exists": True}) if self.agent_exists else (404, None)
        if label == "create owned prompt-agent version":
            self.case.assertEqual(method, "POST")
            self.case.assertTrue(path.endswith("/versions?api-version=v1"))
            definition = {"kind": "prompt", "model": self.plan["agentModel"],
                          "instructions": self.plan["agentInstructions"]}
            self.case.assertEqual(kwargs["body"], {"definition": definition, "draft": False})
            if self.malformed_agent:
                return 200, None
            name = path.split("/")[2]
            self.agent = {"name": name, "version": "9", "id": name + ":9"}
            return 200, {**self.agent, "definition": definition, "draft": False}
        if label == "delete only owned prompt-agent version":
            self.case.assertEqual(method, "DELETE")
            self.case.assertEqual(path, f"/agents/{self.agent['name']}/versions/9?api-version=v1")
            self.case.assertNotIn("force", path)
            self.case.assertIsNone(kwargs.get("body"))
            if self.bad_agent_delete:
                return 200, {"name": self.agent["name"], "version": self.agent["version"], "deleted": False}
            return 204, None
        self.case.fail("Unexpected HTTP operation: " + label)

    def delete_owned_eval(self, eval_id, endpoint, tenant, **kwargs):
        self.record("delete only owned evaluation and runs", eval_id, kwargs)
        self.case.assertEqual(eval_id, self.eval_id)
        return {"id": eval_id, "status": "deleted"}


class OwnedPromptTests(unittest.TestCase):
    def drive(self, **kwargs):
        plan, report = plan_for(), {}
        with tempfile.TemporaryDirectory() as root:
            driver = FakeOwnedDriver(self, plan, Path(root), **kwargs)
            try:
                service.owned_prompt_lifecycle(plan, driver, Path(root), report, ROW)
            except (service.Blocked, RuntimeError) as error:
                report["testException"] = str(error)
        return driver, report

    def test_owned_mode_requires_all_operations_and_rejects_ignored_existing_name(self):
        plan, env = plan_for(), test_service.ServiceTests().github_env()
        service.validate_plan(plan, "b" * 64, env)
        with self.assertRaisesRegex(service.Blocked, "exact lifecycle"):
            service.validate_plan({**plan, "authorizedOperations": plan["authorizedOperations"][:-1]}, "b" * 64, env)
        with self.assertRaisesRegex(service.Blocked, "unknown fields"):
            service.validate_plan({**plan, "datasetName": "must-not-be-ignored"}, "b" * 64, env)

    def test_full_owned_chain_uses_returned_version_and_one_cleanup_budget(self):
        driver, report = self.drive()
        self.assertNotIn("testException", report)
        self.assertEqual(report["quality"], "PASS")
        self.assertEqual(report["ownedAgentVersion"]["version"], "9")
        self.assertEqual(report["remoteCleanup"]["status"], "PASS")
        self.assertEqual(report["datasetCleanup"]["status"], "PASS")
        self.assertEqual(report["agentCleanup"]["status"], "PASS")
        self.assertEqual(len(driver.cleanup_deadlines), 3)
        self.assertEqual(len(set(driver.cleanup_deadlines)), 1)
        labels = [label for label, _ in driver.calls]
        self.assertEqual(labels.count("create owned prompt-agent version"), 1)
        self.assertEqual(labels.count("create owned manual dataset version"), 1)
        self.assertEqual(labels[-3:], ["delete only owned evaluation and runs",
                                     "delete only owned dataset version", "delete only owned prompt-agent version"])

    def test_existing_agent_is_never_adopted_modified_or_deleted(self):
        driver, report = self.drive(agent_exists=True)
        self.assertIn("already exists", report["testException"])
        self.assertFalse(any(label.startswith(("create", "delete")) for label, _ in driver.calls))

    def test_ambiguous_agent_post_is_not_retried_or_deleted_by_guess(self):
        for args in ({"failure": "create owned prompt-agent version"}, {"malformed_agent": True}):
            with self.subTest(args=args):
                driver, report = self.drive(**args)
                self.assertEqual(sum(label == "create owned prompt-agent version" for label, _ in driver.calls), 1)
                self.assertEqual(report["agentCleanup"]["status"], "BLOCKED")
                self.assertTrue(report["agentCleanup"]["manualReconciliationRequired"])
                self.assertFalse(any(label.startswith("delete") for label, _ in driver.calls))

    def test_ambiguous_dataset_write_preserves_receipt_and_cleans_only_confirmed_agent(self):
        driver, report = self.drive(failure="create owned manual dataset version")
        self.assertEqual(report["datasetCleanup"]["status"], "BLOCKED")
        self.assertTrue(report["datasetCleanup"]["manualReconciliationRequired"])
        self.assertEqual(report["agentCleanup"]["status"], "PASS")
        self.assertNotIn("delete only owned dataset version", [label for label, _ in driver.calls])

    def test_roundtrip_content_mismatch_stops_eval_and_cleans_created_resources(self):
        driver, report = self.drive(wrong_row=True)
        self.assertNotIn("create owned evaluation", [label for label, _ in driver.calls])
        self.assertEqual(report["datasetCleanup"]["status"], "PASS")
        self.assertEqual(report["agentCleanup"]["status"], "PASS")

    def test_all_cleanup_attempts_preserve_primary_failure(self):
        driver, report = self.drive(failure="export owned run", cleanup_fails=True)
        self.assertIn("export owned run", report["failure"]["message"])
        for key in ("remoteCleanup", "datasetCleanup", "agentCleanup"):
            self.assertEqual(report[key]["status"], "FAIL")
        self.assertEqual([label for label, _ in driver.calls][-3:], [
            "delete only owned evaluation and runs", "delete only owned dataset version",
            "delete only owned prompt-agent version",
        ])

    def test_success_status_with_negative_agent_delete_body_is_not_cleanup_success(self):
        _, report = self.drive(bad_agent_delete=True)
        self.assertEqual(report["agentCleanup"]["status"], "FAIL")
        self.assertIn("cleanup failed", report["testException"])

    def test_invalid_manual_row_stops_before_install_identity_or_driver(self):
        with tempfile.TemporaryDirectory() as root:
            root = Path(root)
            rows = root / "input.jsonl"
            rows.write_bytes(b'{"query":"q","response":"not a manual prompt fixture"}\n')
            plan = plan_for(rows)
            plan["datasetSha256"] = service.scenario.sha256(rows.read_bytes())
            raw = json.dumps(plan).encode()
            plan_path = root / "plan.json"
            plan_path.write_bytes(raw)
            env = {**test_service.ServiceTests().github_env(),
                   "AZD_SCENARIO_LIVE_APPROVAL_SHA256": service.scenario.sha256(raw)}
            with mock.patch.object(service, "verify_install") as verify, \
                 mock.patch.object(service, "Driver") as driver:
                with self.assertRaisesRegex(service.Blocked, "query/ground_truth"):
                    service.execute(plan_path, root / "evidence", env)
                verify.assert_not_called()
                driver.assert_not_called()
            report = json.loads((root / "evidence" / "service-status.json").read_text())
            self.assertEqual(report["agentCleanup"]["status"], "NOT RUN")
            self.assertEqual(report["datasetCleanup"]["status"], "NOT RUN")

    def test_execute_persists_success_only_after_all_resource_and_local_cleanup(self):
        with tempfile.TemporaryDirectory() as root:
            root = Path(root)
            rows = root / "input.jsonl"
            rows.write_bytes(ROW)
            plan = plan_for(rows)
            raw = json.dumps(plan).encode()
            plan_path = root / "plan.json"
            plan_path.write_bytes(raw)
            auth = root / "auth"
            auth.mkdir()
            env = {**test_service.ServiceTests().github_env(),
                   "AZD_SCENARIO_LIVE_APPROVAL_SHA256": service.scenario.sha256(raw),
                   "AZD_SCENARIO_LIVE_AUTH_CONFIG": str(auth)}
            with mock.patch.object(service, "verify_install", return_value=Path("azd")), \
                 mock.patch.object(service, "Driver", side_effect=lambda exe, cfg, workspace, timeout, duration, report:
                                   FakeOwnedDriver(self, plan, workspace)):
                report = service.execute(plan_path, root / "evidence", env)
            self.assertEqual(report["status"], "PASS")
            self.assertEqual(report["cleanup"]["status"], "PASS")
            self.assertEqual(rows.read_bytes(), ROW)
            self.assertTrue(report["executorImplemented"]["agentVersionCreate"])
            self.assertFalse(report["executorImplemented"]["agentInfrastructureDeploy"])

    def test_real_http_driver_serializes_only_the_minimal_v1_request(self):
        with tempfile.TemporaryDirectory() as root:
            workspace = Path(root)
            report = {}
            driver = service.Driver(Path("azd"), workspace / "auth", workspace, 30, 120, report)
            token = subprocess.CompletedProcess([], 0, b'{"token":"mock-private-credential"}', b"")
            body = {"definition": {"kind": "prompt", "model": "existing", "instructions": "Private test text."},
                    "draft": False}
            response = subprocess.CompletedProcess([], 0, json.dumps({
                "status": 200, "body": base64.b64encode(
                    b'{"name":"ci-owned","version":"8","id":"ci-owned:8"}').decode(),
            }).encode(), b"")
            with mock.patch.object(service.subprocess, "run", return_value=token) as run, \
                 mock.patch.object(service, "transport_exchange", return_value=response) as transport:
                status, result = driver.request(
                    "create owned prompt-agent version", "POST",
                    "https://fixture.services.ai.azure.com/api/projects/fixture",
                    "/agents/ci-owned/versions?api-version=v1", "tenant", body=body, accepted=(200, 201))
            self.assertEqual(status, 200)
            self.assertEqual(result["version"], "8")
            run.assert_called_once()
            transport.assert_called_once()
            request = json.loads(transport.call_args.args[0])
            self.assertEqual(request["method"], "POST")
            self.assertTrue(request["url"].endswith("/agents/ci-owned/versions?api-version=v1"))
            self.assertEqual(request["body"], body)
            self.assertNotIn("mock-private-credential", repr(run.call_args.args))
            self.assertNotIn("mock-private-credential", repr(transport.call_args.args[2]))
            self.assertNotIn("mock-private-credential", json.dumps(report))
            self.assertNotIn("Private test text", json.dumps(report))

    def test_real_http_driver_does_not_retry_an_uncertain_post(self):
        with tempfile.TemporaryDirectory() as root:
            workspace = Path(root)
            driver = service.Driver(Path("azd"), workspace / "auth", workspace, 30, 120, {})
            token = subprocess.CompletedProcess([], 0, b'{"token":"mock-private-credential"}', b"")
            response = subprocess.CompletedProcess([], 0, b'{"status":503,"body":""}', b"")
            with mock.patch.object(service.subprocess, "run", return_value=token) as run, \
                 mock.patch.object(service, "transport_exchange", return_value=response) as transport:
                with self.assertRaisesRegex(RuntimeError, "HTTP 503"):
                    driver.request("create owned prompt-agent version", "POST",
                                   "https://fixture.services.ai.azure.com/api/projects/fixture",
                                   "/agents/ci-owned/versions?api-version=v1", "tenant",
                                   body={"definition": {}}, accepted=(200, 201))
            run.assert_called_once()
            transport.assert_called_once()


if __name__ == "__main__":
    unittest.main()
