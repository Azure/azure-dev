# Copyright (c) Microsoft Corporation. All rights reserved.
# Licensed under the MIT License.

import json
from pathlib import Path
import subprocess
import tempfile
import unittest
from unittest import mock

import service
import test_owned_prompt
import test_service


def plan_for():
    plan = test_owned_prompt.plan_for()
    del plan["agentModel"], plan["agentInstructions"]
    plan.update(mode=service.AGENT_CLI_MODE, agentName="approved-existing-agent", agentVersion="9",
                agentInputField="query", agentResponseField="answer")
    plan["versions"][service.AGENT_EXTENSION] = "1.0.0-beta.16"
    plan["binarySha256"][service.AGENT_EXTENSION] = "d" * 64
    plan["authorizedOperations"] = [
        "agent-session-create", "agent-invoke", "agent-session-delete", "dataset-create", "dataset-delete",
        "dataset-download", "eval-create", "eval-run", "eval-export", "eval-delete",
    ]
    return plan


class FakeCliDriver:
    def __init__(self, case, workspace, failure=None, cleanup_fails=False):
        self.case, self.workspace, self.failure = case, workspace, failure
        self.cleanup_fails = cleanup_fails
        self.calls, self.deadlines = [], []
        self.plan = plan_for()
        self.session = self.dataset = self.row = None

    def record(self, label, kwargs):
        self.calls.append(label)
        if "cleanup_deadline" in kwargs:
            self.deadlines.append(kwargs["cleanup_deadline"])
        if label == self.failure or (self.cleanup_fails and label.startswith(("delete", "clear"))):
            raise RuntimeError(label + " failed")

    def __call__(self, label, args, **kwargs):
        self.record(label, kwargs)
        if label == "verify existing service identity":
            return {"status": "authenticated", "type": "servicePrincipal", "clientId": self.plan["clientId"]}
        if label == "verify native service token identity":
            return test_service.ServiceTests().identity(self.plan)
        if label.startswith("verify approved"):
            extension = next(key for key, value in service.required_extensions(self.plan).items() if value == args[1])
            if extension == service.AGENT_EXTENSION:
                self.case.assertIsNone(kwargs["output_format"])
                return f"Version: {self.plan['versions'][extension]}\nCommit: test-only\nBuild Date: test-only\n".encode()
            return {"name": extension, "version": self.plan["versions"][extension]}
        if label == "invoke approved existing agent through CLI":
            self.case.assertEqual(args[:3], ["ai", "agent", "invoke"])
            self.case.assertEqual(kwargs["output_format"], "raw")
            self.case.assertEqual(args[args.index("--session-id") + 1], self.session)
            self.case.assertNotIn("--version", args)
            self.case.assertNotIn("--protocol", args)
            self.case.assertEqual(json.loads(Path(args[args.index("--input-file") + 1]).read_bytes()),
                                  {"query": json.loads(test_owned_prompt.ROW)["query"]})
            return b'HTTP/1.1 200 OK\r\nContent-Type: application/json\r\n\r\n{"answer":"blue"}'
        if label == "create owned invocation dataset version":
            self.dataset = {"name": args[3], "version": args[args.index("--version") + 1]}
            self.row = Path(args[args.index("--from-file") + 1]).read_bytes()
            self.case.assertEqual(json.loads(self.row), {**json.loads(test_owned_prompt.ROW), "response": "blue"})
            return self.dataset
        if label == "download approved registered version":
            Path(args[args.index("--output-file") + 1]).write_bytes(self.row)
            return None
        if label == "create owned evaluation":
            config = json.loads((self.workspace / "azure.eval.yaml").read_bytes())
            self.case.assertNotIn("target", config["evals"][0])
            return {"id": "eval-owned", "name": args[3]}
        if label == "start one authorized run":
            return {"eval_id": "eval-owned", "run_id": "run-owned"}
        if label == "wait for owned run":
            return {"id": "run-owned", "status": "completed", "result_counts": {"total": 1, "passed": 1}}
        if label == "export owned run":
            return {"run": {"id": "run-owned"},
                    "items": [{"run_id": "run-owned", "datasource_item": json.loads(self.row)}]}
        if label == "delete only owned dataset version":
            self.case.assertEqual(args[3], self.dataset["name"])
            return {**self.dataset, "status": "deleted"}
        self.case.fail("Unexpected command: " + label)

    def request(self, label, method, endpoint, path, tenant, **kwargs):
        self.record(label, kwargs)
        self.case.assertEqual(endpoint, self.plan["projectEndpoint"])
        prefix = "/agents/approved-existing-agent/endpoint/sessions"
        if label == "create owned version-bound session":
            self.case.assertEqual((method, path), ("POST", prefix + "?api-version=v1"))
            self.session = kwargs["body"]["agent_session_id"]
            return 201, {"agent_session_id": self.session, "version_indicator": kwargs["body"]["version_indicator"]}
        self.case.assertEqual(label, "delete only owned invocation session")
        self.case.assertEqual((method, path), ("DELETE", prefix + "/" + self.session + "?api-version=v1"))
        self.case.assertFalse(kwargs["read_json"])
        return 204, None

    def delete_owned_eval(self, eval_id, endpoint, tenant, **kwargs):
        self.record("delete only owned evaluation and runs", kwargs)
        self.case.assertEqual(eval_id, "eval-owned")
        return {"id": eval_id, "status": "deleted"}

    def clear_agent_state(self, **kwargs):
        self.record("clear owned local agent state", kwargs)


class AgentCliTests(unittest.TestCase):
    def drive(self, **kwargs):
        report = {}
        with tempfile.TemporaryDirectory() as root:
            driver = FakeCliDriver(self, Path(root), **kwargs)
            try:
                service.existing_agent_cli_lifecycle(driver.plan, driver, Path(root), report, test_owned_prompt.ROW)
            except RuntimeError as error:
                report["testException"] = str(error)
        return driver, report

    def test_real_cli_contract_feeds_actual_invocation_response_to_evaluation(self):
        driver, report = self.drive()
        self.assertNotIn("testException", report)
        self.assertEqual(report["quality"], "PASS")
        self.assertEqual(report["agentCliInvocation"]["status"], "PASS")
        for key in ("remoteCleanup", "datasetCleanup", "sessionCleanup", "agentStateCleanup"):
            self.assertEqual(report[key]["status"], "PASS")
        self.assertEqual(len(set(driver.deadlines)), 1)
        self.assertEqual(driver.calls.count("invoke approved existing agent through CLI"), 1)
        self.assertFalse(any("prompt-agent" in label or "deploy" in label for label in driver.calls))
        self.assertNotIn('"blue"', json.dumps(report))

    def test_ambiguous_session_create_never_invokes_retries_or_deletes_guessed_id(self):
        driver, report = self.drive(failure="create owned version-bound session")
        self.assertEqual(report["sessionCleanup"]["status"], "BLOCKED")
        self.assertTrue(report["sessionCleanup"]["manualReconciliationRequired"])
        self.assertFalse(any(label.startswith(("delete", "invoke")) for label in driver.calls))

    def test_invocation_failure_cleans_session_without_dataset_or_eval(self):
        driver, report = self.drive(failure="invoke approved existing agent through CLI")
        self.assertEqual(report["sessionCleanup"]["status"], "PASS")
        self.assertNotIn("create owned invocation dataset version", driver.calls)
        self.assertNotIn("quality", report)

    def test_wrong_session_version_cleans_confirmed_id_without_invocation(self):
        request = FakeCliDriver.request

        def wrong_version(driver, label, *args, **kwargs):
            status, result = request(driver, label, *args, **kwargs)
            if label == "create owned version-bound session":
                result["version_indicator"]["agent_version"] = "wrong"
            return status, result

        with mock.patch.object(FakeCliDriver, "request", wrong_version):
            driver, report = self.drive()
        self.assertEqual(report["sessionCleanup"]["status"], "PASS")
        self.assertNotIn("invoke approved existing agent through CLI", driver.calls)

    def test_ambiguous_dataset_create_cleans_only_the_confirmed_session(self):
        driver, report = self.drive(failure="create owned invocation dataset version")
        self.assertEqual(report["datasetCleanup"]["status"], "BLOCKED")
        self.assertTrue(report["datasetCleanup"]["manualReconciliationRequired"])
        self.assertEqual(report["sessionCleanup"]["status"], "PASS")
        self.assertNotIn("delete only owned dataset version", driver.calls)
        self.assertNotIn("create owned evaluation", driver.calls)

    def test_primary_failure_and_every_cleanup_failure_are_retained(self):
        driver, report = self.drive(failure="export owned run", cleanup_fails=True)
        self.assertIn("export owned run", report["failure"]["message"])
        for key in ("remoteCleanup", "datasetCleanup", "sessionCleanup", "agentStateCleanup"):
            self.assertEqual(report[key]["status"], "FAIL")
        self.assertEqual(len(driver.deadlines), 4)
        self.assertEqual(len(set(driver.deadlines)), 1)

    def test_raw_http_success_is_not_itself_invocation_success(self):
        responses = [
            b'HTTP/1.1 202 Accepted\r\nContent-Type: application/json\r\n\r\n{"answer":"blue"}',
            b'HTTP/1.1 200 OK\r\nContent-Type: text/event-stream\r\n\r\ndata: {"answer":"blue"}',
            b'HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nContent-Type: application/json\r\n\r\n{}',
            b'HTTP/1.1 200 OK\r\nContent-Type: application/json\r\n\r\n{"error":{},"answer":"blue"}',
            b'HTTP/1.1 200 OK\r\nContent-Type: application/json\r\n\r\n{"answer":""}',
            b'HTTP/1.1 200 OK\r\nContent-Type: application/json\r\n\r\n{"answer":"first","answer":"second"}',
        ]
        for raw in responses:
            with self.subTest(raw=raw), self.assertRaises(RuntimeError):
                service.invocation_response(raw, "answer")

    def test_approval_requires_agent_binary_and_exact_operations(self):
        plan, env = plan_for(), test_service.ServiceTests().github_env()
        service.validate_plan(plan, "b" * 64, env)
        for changes in ({"agentVersion": ""}, {"agentName": "../shared"}, {"agentResponseField": "error"},
                        {"versions": test_service.ServiceTests().plan()["versions"]},
                        {"authorizedOperations": plan["authorizedOperations"] + ["agent-delete"]}):
            with self.subTest(changes=changes), self.assertRaises(service.Blocked):
                service.validate_plan({**plan, **changes}, "b" * 64, env)

    def test_driver_invocation_uses_raw_not_unsupported_json_and_redacts_endpoint(self):
        with tempfile.TemporaryDirectory() as root:
            report = {}
            driver = service.Driver(Path("azd"), Path(root) / "auth", Path(root), 15, 30, report)
            response = b'HTTP/1.1 200 OK\r\nContent-Type: application/json\r\n\r\n{"answer":"private-value"}'
            with mock.patch.object(service, "run_owned_process",
                                   return_value=subprocess.CompletedProcess([], 0, response, b"")) as run:
                result = driver("invoke", ["ai", "agent", "invoke", "--agent-endpoint",
                                           "https://private.services.ai.azure.com/private"], output_format="raw")
            self.assertEqual(result, response)
            self.assertEqual(run.call_args.args[0][-3:], ["--no-prompt", "--output", "raw"])
            self.assertNotIn("private-value", json.dumps(report))
            self.assertNotIn("private.services", json.dumps(report))

    def test_agents_version_uses_the_actual_text_contract_not_json(self):
        plan = plan_for()
        for raw, accepted in (
            (b"Version: 1.0.0-beta.16\nCommit: fixture\nBuild Date: fixture\n", True),
            (b"Version: 1.0.0-beta.16\r\nCommit: fixture\r\nBuild Date: fixture\r\n", True),
            (b"Version: 1.0.0-beta.17\nCommit: fixture\nBuild Date: fixture\n", False),
            (b'{"name":"azure.ai.agents","version":"1.0.0-beta.16"}', False),
            (b"Version: 1.0.0-beta.16\n", False),
        ):
            with self.subTest(raw=raw), tempfile.TemporaryDirectory() as root:
                fake = FakeCliDriver(self, Path(root))
                command = fake.__call__

                def driver(label, args, **kwargs):
                    return raw if label == "verify approved " + service.AGENT_EXTENSION else command(label, args, **kwargs)

                if accepted:
                    service.verify_identity(plan, driver)
                else:
                    with self.assertRaisesRegex(service.Blocked, "Agents runtime text version"):
                        service.verify_identity(plan, driver)
        with tempfile.TemporaryDirectory() as root:
            driver = service.Driver(Path("azd"), Path(root) / "auth", Path(root), 15, 30, {})
            with mock.patch.object(service, "run_owned_process",
                                   return_value=subprocess.CompletedProcess([], 0, b"version text", b"")) as run:
                self.assertEqual(driver("version", ["ai", "agent", "version"], output_format=None), b"version text")
            self.assertEqual(run.call_args.args[0], ["azd", "ai", "agent", "version", "--no-prompt"])

    def installed_fixture(self, root):
        plan, settings = test_service.ServiceTests().installed_fixture(root)
        del plan["datasetName"]
        plan.update({key: value for key, value in plan_for().items() if key not in ("azdExecutable", "binarySha256")})
        binary = root / "agent.exe"
        binary.write_bytes(b"approved agent bytes")
        plan["binarySha256"][service.AGENT_EXTENSION] = service.scenario.sha256(binary.read_bytes())
        settings["extension"]["installed"][service.AGENT_EXTENSION] = {
            "id": service.AGENT_EXTENSION, "namespace": "ai.agent",
            "version": plan["versions"][service.AGENT_EXTENSION], "path": binary.name,
        }
        (root / "config.json").write_text(json.dumps(settings))
        return plan, settings, binary

    def test_agent_bytes_and_empty_local_state_are_checked_before_driver(self):
        for kind in ("binary", "state"):
            with self.subTest(kind=kind), tempfile.TemporaryDirectory() as root:
                root = Path(root)
                plan, settings, binary = self.installed_fixture(root)
                service.verify_install(plan, root)
                if kind == "binary":
                    binary.write_bytes(b"unapproved agent bytes")
                else:
                    settings["extensions"] = {"ai-agents": {"sessions": {"existing": "do-not-delete"}}}
                    (root / "config.json").write_text(json.dumps(settings))
                row = root / "manual.jsonl"
                row.write_bytes(test_owned_prompt.ROW)
                plan["datasetFile"] = str(row)
                raw = json.dumps(plan).encode()
                path = root / "plan.json"
                path.write_bytes(raw)
                env = {**test_service.ServiceTests().github_env(),
                       "AZD_SCENARIO_LIVE_APPROVAL_SHA256": service.scenario.sha256(raw),
                       "AZD_SCENARIO_LIVE_AUTH_CONFIG": str(root)}
                with mock.patch.object(service, "Driver") as driver:
                    with self.assertRaises(service.Blocked):
                        service.execute(path, root / "evidence", env)
                    driver.assert_not_called()
                receipt = json.loads((root / "evidence" / "service-status.json").read_bytes())
                self.assertEqual((receipt["status"], receipt["execution"]), ("BLOCKED", "NOT RUN"))
                for field in ("sessionCleanup", "agentStateCleanup", "agentCliInvocation"):
                    self.assertEqual(receipt[field]["status"], "NOT RUN")

    def test_pre_lifecycle_blocks_keep_all_agent_operation_states(self):
        for kind in ("approval", "manual-row"):
            with self.subTest(kind=kind), tempfile.TemporaryDirectory() as root:
                root = Path(root)
                row = root / "manual.jsonl"
                row.write_bytes(test_owned_prompt.ROW if kind == "approval" else b'{"query":"invalid"}\n')
                plan = plan_for()
                plan["datasetFile"] = str(row)
                raw = json.dumps(plan).encode()
                path = root / "plan.json"
                path.write_bytes(raw)
                env = {**test_service.ServiceTests().github_env(),
                       "AZD_SCENARIO_LIVE_APPROVAL_SHA256":
                           "0" * 64 if kind == "approval" else service.scenario.sha256(raw)}
                with mock.patch.object(service, "verify_install") as verify, \
                     mock.patch.object(service, "Driver") as driver:
                    with self.assertRaises(service.Blocked):
                        service.execute(path, root / "evidence", env)
                    verify.assert_not_called()
                    driver.assert_not_called()
                receipt = json.loads((root / "evidence" / "service-status.json").read_bytes())
                self.assertEqual((receipt["status"], receipt["execution"]), ("BLOCKED", "NOT RUN"))
                for field in ("remoteCleanup", "datasetCleanup", "agentCleanup",
                              "sessionCleanup", "agentStateCleanup", "agentCliInvocation", "cleanup"):
                    self.assertEqual(receipt[field]["status"], "NOT RUN")
                self.assertNotIn("commands", receipt)

    def test_execute_wires_cli_mode_and_waits_for_all_cleanup(self):
        with tempfile.TemporaryDirectory() as root:
            root = Path(root)
            plan, _, _ = self.installed_fixture(root)
            row = root / "manual.jsonl"
            row.write_bytes(test_owned_prompt.ROW)
            plan["datasetFile"] = str(row)
            raw = json.dumps(plan).encode()
            path = root / "plan.json"
            path.write_bytes(raw)
            env = {**test_service.ServiceTests().github_env(),
                   "AZD_SCENARIO_LIVE_APPROVAL_SHA256": service.scenario.sha256(raw),
                   "AZD_SCENARIO_LIVE_AUTH_CONFIG": str(root)}
            with mock.patch.object(service, "Driver", side_effect=lambda exe, config, workspace, *args:
                                   FakeCliDriver(self, workspace)):
                result = service.execute(path, root / "evidence", env)
            self.assertEqual((result["status"], result["execution"]), ("PASS", "COMPLETED"))
            self.assertEqual(result["cleanup"]["status"], "PASS")
            self.assertEqual(result["agentStateCleanup"]["status"], "PASS")
            self.assertTrue(result["executorImplemented"]["agentCliInvoke"])
            self.assertFalse(result["executorImplemented"]["agentInfrastructureDeploy"])

    def test_cleanup_removes_only_preflight_empty_agent_namespace_and_verifies_readback(self):
        with tempfile.TemporaryDirectory() as root:
            root = Path(root)
            profile = root / "auth"
            profile.mkdir()
            config = profile / "config.json"
            untouched = {"extension": {"installed": {"fixture": {}}}, "sentinel": "preserved"}
            settings = {**untouched, "extensions": {"ai-agents": {"sessions": {"owned": "id"}}, "other": "keep"}}
            config.write_text(json.dumps(settings))
            driver = service.Driver(Path("azd"), profile, root, 30, 60, {})

            def unset(*args, **kwargs):
                self.assertEqual(args[0][-5:], ["unset", "extensions.ai-agents", "--no-prompt", "--output", "json"])
                del settings["extensions"]["ai-agents"]
                config.write_text(json.dumps(settings))
                return subprocess.CompletedProcess([], 0, b"", b"")

            with mock.patch.object(service, "run_owned_process", side_effect=unset):
                driver.clear_agent_state(cleanup_deadline=service.time.monotonic() + 30)
            self.assertEqual(json.loads(config.read_bytes()), {**untouched, "extensions": {"other": "keep"}})


if __name__ == "__main__":
    unittest.main()
