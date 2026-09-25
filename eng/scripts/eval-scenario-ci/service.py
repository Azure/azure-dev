#!/usr/bin/env python3
# Copyright (c) Microsoft Corporation. All rights reserved.
# Licensed under the MIT License.

"""Opt-in evaluation lifecycles using pre-authorized, existing projects/models.

This module is executable but is not activated by the shipped provider jobs.
Only mock-command tests are authorized in this contribution. Authentication,
service-side budget verification and provider approval remain external gates.
"""

import argparse
import base64
from datetime import datetime, timezone
from decimal import Decimal, InvalidOperation
import hashlib
import json
import os
from pathlib import Path, PureWindowsPath
import re
import subprocess
import sys
import threading
import time
import urllib.parse
import uuid

import scenario

HTTP_TRANSPORT = Path(__file__).with_name("http_transport.py")


def transport_exchange(encoded, deadline, environment, workspace):
    """Bound pipe writes, response reads and exit with one owned-process deadline."""
    args = [sys.executable, str(HTTP_TRANSPORT)]
    expired = threading.Event()
    started = time.monotonic()
    with subprocess.Popen(args, cwd=workspace, env=environment, stdin=subprocess.PIPE,
                          stdout=subprocess.PIPE, stderr=subprocess.PIPE) as process:
        if time.monotonic() >= deadline:
            process.kill()
            process.wait()
            raise subprocess.TimeoutExpired(args, max(0, deadline - started))

        def terminate():
            if process.poll() is None:
                expired.set()
                try:
                    process.kill()
                except ProcessLookupError:
                    pass

        timer = threading.Timer(max(0, deadline - time.monotonic()), terminate)
        timer.daemon = True
        timer.start()
        try:
            stdout, stderr = process.communicate(input=encoded)
        finally:
            timer.cancel()
            if process.poll() is None:
                process.kill()
            process.wait()
            timer.join()
    if expired.is_set() or time.monotonic() >= deadline:
        raise subprocess.TimeoutExpired(args, max(0, deadline - started))
    return subprocess.CompletedProcess(args, process.returncode, stdout, stderr)


class Blocked(RuntimeError):
    pass


def require(condition, message):
    if not condition:
        raise Blocked(message)


def expect(condition, message):
    if not condition:
        raise RuntimeError(message)


def unique_plan_object(pairs):
    value = {}
    for key, item in pairs:
        require(key not in value, "Approved plans must not contain duplicate JSON object keys")
        value[key] = item
    return value


def same_json_value(actual, expected):
    if type(actual) in (int, Decimal) and type(expected) in (int, Decimal):
        return actual == expected
    if type(actual) is not type(expected):
        return False
    if isinstance(expected, dict):
        return actual.keys() == expected.keys() and all(
            same_json_value(actual[key], value) for key, value in expected.items())
    if isinstance(expected, list):
        return len(actual) == len(expected) and all(
            same_json_value(left, right) for left, right in zip(actual, expected))
    return actual == expected


def verify_native_identity_token(value, plan):
    require(isinstance(value, dict) and isinstance(value.get("token"), str),
            "Native CI identity did not provide a token for identity verification")
    parts = value["token"].split(".")
    require(len(parts) == 3, "Native identity token format cannot be verified")
    try:
        claims = json.loads(base64.urlsafe_b64decode(parts[1] + "=" * (-len(parts[1]) % 4)))
    except (ValueError, UnicodeError):
        raise Blocked("Native identity token claims could not be decoded") from None
    require(isinstance(claims, dict), "Native identity token claims must be an object")
    client = claims.get("appid", claims.get("azp"))
    tenant = claims.get("tid")
    require(isinstance(client, str) and isinstance(tenant, str)
            and client.lower() == plan["clientId"].lower() and tenant.lower() == plan["tenantId"].lower(),
            "Native service identity client/tenant does not match the approved plan")


def validate_ci_identity(plan, env):
    variables = {
        "github": {
            "repository": "GITHUB_REPOSITORY", "repositoryId": "GITHUB_REPOSITORY_ID",
            "workflowRef": "GITHUB_WORKFLOW_REF", "workflowSha": "GITHUB_WORKFLOW_SHA",
            "job": "GITHUB_JOB", "attempt": "GITHUB_RUN_ATTEMPT",
        },
        "azure-devops": {
            "collectionUri": "SYSTEM_COLLECTIONURI", "collectionId": "SYSTEM_COLLECTIONID",
            "projectId": "SYSTEM_TEAMPROJECTID", "repositoryId": "BUILD_REPOSITORY_ID",
            "repositoryProvider": "BUILD_REPOSITORY_PROVIDER", "definitionId": "SYSTEM_DEFINITIONID",
            "jobId": "SYSTEM_JOBID", "attempt": "SYSTEM_JOBATTEMPT",
        },
    }
    require(plan["provider"] in variables, "Live execution requires a supported CI provider")
    trusted = {name: env.get(variable) for name, variable in variables[plan["provider"]].items()}
    require(all(isinstance(value, str) and value for value in trusted.values()),
            "Required native CI repository/workflow identity variables are unavailable")
    identity = plan["ciIdentity"]
    require(isinstance(identity, dict) and identity == trusted,
            "Approved CI identity does not match this repository, workflow, job and attempt")


def validate_plan(plan, digest, env):
    require(isinstance(plan, dict) and plan.get("mode") in ("static-evaluation", "owned-prompt-evaluation"),
            "Only static evaluation or owned prompt-version evaluation is implemented")
    owned_prompt = plan["mode"] == "owned-prompt-evaluation"
    allowed = {
        "schemaVersion", "mode", "provider", "workflowCommit", "runId", "expiresAt", "authorizedOperations",
        "approvalReference", "resourceOwner", "budgetControlReference", "budgetControlExternallyVerified",
        "approvedBudget", "maxRuns", "maxRows", "timeoutSeconds", "clientId", "tenantId",
        "datasetVersion", "datasetSha256", "evaluator", "judgeModel", "ownerPrefix", "projectEndpoint",
        "azdExecutable", "binarySha256", "versions",
        "durationSeconds", "ciIdentity",
    }
    allowed.update({"datasetFile", "agentModel", "agentInstructions"} if owned_prompt else {"datasetName"})
    require(isinstance(plan, dict) and set(plan) == allowed,
            "Plan contains unknown fields or omits required fields")
    for key in ("mode", "provider", "workflowCommit", "runId", "expiresAt", "approvalReference",
                "resourceOwner", "budgetControlReference", "clientId", "tenantId", "projectEndpoint",
                "azdExecutable"):
        require(isinstance(plan[key], str) and plan[key], f"Invalid {key}")
    require(env.get("AZD_SCENARIO_LIVE_APPROVAL_SHA256") == digest,
            "The exact plan lacks an externally supplied approval digest")
    require(type(plan.get("schemaVersion")) is int and plan["schemaVersion"] == 1,
            "Unsupported approval-plan schema")
    validate_ci_identity(plan, env)
    provider = scenario.run_identity(env)
    require(provider["provider"] in ("github", "azure-devops"), "Live execution is CI-only")
    require(plan["provider"] == provider["provider"] and plan["workflowCommit"] == provider["workflowCommit"],
            "Plan does not match this CI provider and workflow revision")
    run_id = env.get("GITHUB_RUN_ID") if provider["provider"] == "github" else env.get("BUILD_BUILDID")
    require(plan["runId"] == run_id, "Plan belongs to a different CI run")
    expires = datetime.fromisoformat(plan["expiresAt"].replace("Z", "+00:00"))
    remaining = (expires - datetime.now(timezone.utc)).total_seconds() if expires.tzinfo else 0
    require(0 < remaining <= 86400,
            "Plan must expire within 24 hours")
    operations = {"dataset-download", "eval-create", "eval-run", "eval-export", "eval-delete"}
    if owned_prompt:
        operations.update({"agent-version-create", "agent-version-delete", "dataset-create", "dataset-delete"})
    require(isinstance(plan["authorizedOperations"], list)
            and all(isinstance(operation, str) for operation in plan["authorizedOperations"])
            and len(plan["authorizedOperations"]) == len(operations)
            and set(plan["authorizedOperations"]) == operations,
            "The exact lifecycle and its cleanup must be authorized")
    require(plan["approvalReference"] and plan["resourceOwner"] and plan["budgetControlReference"],
            "An explicit approval, resource owner and verified external budget control are required")
    budget = Decimal(str(plan["approvedBudget"]))
    require(budget.is_finite() and budget > 0, "A positive explicit operation budget is required")
    require(plan["budgetControlExternallyVerified"] is True,
            "Service-side budget control has not been verified by the authorizing owner")
    require(type(plan["maxRuns"]) is int and type(plan["maxRows"]) is int
            and plan["maxRuns"] == 1 and plan["maxRows"] == 1,
            "Only one run of one registered row is implemented")
    require(type(plan["timeoutSeconds"]) is int and 1 <= plan["timeoutSeconds"] <= 600,
            "Command timeout must be 1..600 seconds")
    require(type(plan["durationSeconds"]) is int and 1 <= plan["durationSeconds"] <= 900,
            "Total observation duration must be 1..900 seconds")
    require(remaining > plan["durationSeconds"] + plan["timeoutSeconds"] + 30,
            "Approval must leave time for observation, owned cleanup and setup overhead")
    for key in ("clientId", "tenantId"):
        require(str(uuid.UUID(plan[key])) == plan[key].lower(), f"Invalid {key}")
    for key in ("datasetVersion", "evaluator", "judgeModel", "ownerPrefix"):
        require(isinstance(plan[key], str) and re.fullmatch(r"[A-Za-z0-9][A-Za-z0-9._/-]{0,100}", plan[key]),
                f"Invalid {key}")
    if owned_prompt:
        require(isinstance(plan["datasetFile"], str) and plan["datasetFile"],
                "An approved manual JSONL file is required")
        require(isinstance(plan["agentModel"], str)
                and re.fullmatch(r"[A-Za-z0-9][A-Za-z0-9._-]{0,100}", plan["agentModel"]),
                "An existing prompt-agent model deployment is required")
        require(isinstance(plan["agentInstructions"], str) and plan["agentInstructions"].strip(),
                "Explicit plain-text prompt-agent instructions are required")
    else:
        require(isinstance(plan["datasetName"], str)
                and re.fullmatch(r"[A-Za-z0-9][A-Za-z0-9._-]{0,100}", plan["datasetName"]), "Invalid datasetName")
    require(re.fullmatch(r"ci-[a-z0-9-]{1,20}", plan["ownerPrefix"]), "Owned names require a short ci- prefix")
    require(plan["evaluator"].startswith("builtin."), "Custom evaluator publication is not implemented")
    require(isinstance(plan["versions"], dict) and set(plan["versions"]) == set(scenario.EXTENSIONS)
            and all(isinstance(version, str) and version for version in plan["versions"].values()),
            "Both approved extension versions are required")
    require(isinstance(plan["datasetSha256"], str) and scenario.HEX.fullmatch(plan["datasetSha256"]),
            "An approved immutable dataset-content digest is required")
    endpoint = urllib.parse.urlsplit(plan["projectEndpoint"])
    require(endpoint.scheme == "https" and endpoint.hostname
            and endpoint.hostname.endswith(".services.ai.azure.com")
            and endpoint.port is None and not endpoint.username and not endpoint.password
            and not endpoint.query and not endpoint.fragment
            and re.fullmatch(r"/api/projects/[A-Za-z0-9._-]+", endpoint.path),
            "An explicit credential-free existing Foundry project endpoint is required")
    return provider


def verify_install(plan, config):
    config = config.resolve()
    executable = Path(plan["azdExecutable"]).resolve()
    expected = plan["binarySha256"]
    require(isinstance(expected, dict) and set(expected) == {"azd", *scenario.EXTENSIONS}
            and all(isinstance(value, str) and scenario.HEX.fullmatch(value) for value in expected.values()),
            "All three installed binary digests are required")
    require(executable.is_file() and scenario.sha256(executable.read_bytes()) == expected["azd"],
            "Core executable does not match the approved bytes")
    settings = json.loads((config / "config.json").read_text(encoding="utf-8-sig"))
    extension_settings = settings.get("extension", {}) if isinstance(settings, dict) else {}
    installed = extension_settings.get("installed", {}) if isinstance(extension_settings, dict) else {}
    require(isinstance(installed, dict) and set(installed) == set(scenario.EXTENSIONS),
            "The isolated profile must contain exactly the two approved extensions")
    for extension, command in scenario.EXTENSIONS.items():
        record = installed[extension]
        require(isinstance(record, dict) and record.get("id") == extension
                and record.get("namespace") == "ai." + command
                and record.get("version") == plan["versions"][extension],
                "Installed extension routing or version metadata differs from the approved plan")
        raw_path = record.get("path")
        require(isinstance(raw_path, str) and raw_path
                and not Path(raw_path).is_absolute() and not PureWindowsPath(raw_path).drive
                and not PureWindowsPath(raw_path).root,
                "Installed extension path must be relative to the isolated profile")
        require(".." not in Path(raw_path).parts and ".." not in PureWindowsPath(raw_path).parts
                and ":" not in raw_path, "Installed extension path must be a contained executable path")
        require(os.name != "nt" or Path(raw_path).suffix.lower() == ".exe",
                "Windows installed extension paths must name an explicit executable")
        binary = (config / raw_path).resolve()
        require(binary.is_relative_to(config), "Installed extension path escapes the isolated profile")
        require(binary.is_file() and scenario.sha256(binary.read_bytes()) == expected[extension],
                "Installed extension does not match the approved bytes")
    return executable


class Driver:
    def __init__(self, executable, config, workspace, timeout, duration, report):
        self.executable, self.workspace, self.timeout, self.report = executable, workspace, timeout, report
        self.deadline = time.monotonic() + duration
        self.env = {key: value for key, value in os.environ.items()
                    if key.upper() in ("PATH", "SYSTEMROOT", "WINDIR", "COMSPEC", "PATHEXT", "LANG")}
        for name in ("home", "temp", "azure"):
            (workspace / name).mkdir()
        self.env.update({
            "HOME": str(workspace / "home"), "USERPROFILE": str(workspace / "home"),
            "TMP": str(workspace / "temp"), "TEMP": str(workspace / "temp"),
            "TMPDIR": str(workspace / "temp"), "AZURE_CONFIG_DIR": str(workspace / "azure"),
            "AZD_CONFIG_DIR": str(config), "AZURE_DEV_COLLECT_TELEMETRY": "no",
            "AZD_FORCE_TTY": "false", "NO_COLOR": "1", "CI": "true",
        })

    def __call__(self, label, args, *, private_output=False, cleanup_deadline=None):
        argv = [str(self.executable), *args, "--no-prompt", "--output", "json"]
        deadline = self.deadline if cleanup_deadline is None else cleanup_deadline
        timeout = min(self.timeout, deadline - time.monotonic())
        if timeout <= 0:
            raise RuntimeError("Command deadline elapsed; remote completion and billing are unknown")
        safe_args = [scenario.proof_module.sanitize(arg, self.workspace) for arg in ["azd", *argv[1:]]]
        if len(safe_args) > 4 and safe_args[1:3] == ["ai", "dataset"] \
                and safe_args[3] in ("create", "download", "delete"):
            safe_args[4] = "<approved-dataset>"
            if "--version" in safe_args:
                safe_args[safe_args.index("--version") + 1] = "<approved-dataset-version>"
        if "--project-endpoint" in safe_args:
            safe_args[safe_args.index("--project-endpoint") + 1] = "<approved-project>"
        if "--tenant-id" in safe_args:
            safe_args[safe_args.index("--tenant-id") + 1] = "<approved-tenant>"
        record = {"name": label, "startedAt": datetime.now(timezone.utc).isoformat(),
                  "timeoutSeconds": timeout, "exitCode": None, "command": safe_args}
        self.report.setdefault("commands", []).append(record)
        started = time.monotonic()
        try:
            result = subprocess.run(argv, cwd=self.workspace, env=self.env, stdin=subprocess.DEVNULL,
                                    capture_output=True, timeout=timeout, check=False)
            record["exitCode"] = result.returncode
            if private_output:
                record["outputOmitted"] = "credential response"
            else:
                record.update(stdoutSha256=hashlib.sha256(result.stdout).hexdigest(),
                              stderrSha256=hashlib.sha256(result.stderr).hexdigest())
            if result.returncode != 0:
                raise RuntimeError(f"{label} returned exit {result.returncode}; raw service output is not published")
            return json.loads(result.stdout, parse_float=Decimal) if result.stdout.strip() else None
        except subprocess.TimeoutExpired:
            record["timedOut"] = True
            raise RuntimeError(f"{label} timed out; remote completion and billing are unknown") from None
        finally:
            record["finishedAt"] = datetime.now(timezone.utc).isoformat()
            record["durationSeconds"] = round(time.monotonic() - started, 6)

    def request(self, label, method, endpoint, path, tenant, *, body=None, accepted=(200,),
                cleanup_deadline=None, read_json=True):
        deadline = self.deadline if cleanup_deadline is None else cleanup_deadline
        credential = self("acquire private service token", [
            "auth", "token", "--scope", "https://ai.azure.com/.default", "--tenant-id", tenant,
        ], private_output=True, cleanup_deadline=deadline)
        require(isinstance(credential, dict) and isinstance(credential.get("token"), str)
                and credential["token"] and not any(char.isspace() for char in credential["token"]),
                "Native CI identity did not provide a usable service token")
        timeout = min(self.timeout, deadline - time.monotonic())
        expect(timeout > 0, "Service command deadline expired")
        record = {"name": label, "method": method, "target": "<approved-project>/<owned-resource>",
                  "startedAt": datetime.now(timezone.utc).isoformat(), "timeoutSeconds": timeout}
        self.report.setdefault("commands", []).append(record)
        started = time.monotonic()
        try:
            payload = {"url": endpoint + path, "method": method, "token": credential["token"],
                       "body": body, "timeout": timeout, "readJson": read_json}
            encoded = json.dumps(payload).encode("utf-8")
            http_deadline = min(deadline, started + self.timeout)
            timeout = http_deadline - time.monotonic()
            expect(timeout > 0, "Service command deadline expired before transport start")
            record["timeoutSeconds"] = timeout
            transport_env = {key: value for key, value in self.env.items()
                             if key not in ("AZD_CONFIG_DIR", "AZURE_CONFIG_DIR")}
            result = transport_exchange(encoded, http_deadline, transport_env, self.workspace)
            expect(result.returncode == 0, f"{label} transport failed; remote outcome is unknown")
            envelope = json.loads(result.stdout)
            expect(isinstance(envelope, dict) and type(envelope.get("status")) is int
                   and isinstance(envelope.get("body"), str), "Invalid private transport response")
            status = envelope["status"]
            raw = base64.b64decode(envelope["body"], validate=True)
            record["httpStatus"] = status
            expect(status in accepted, f"{label} returned HTTP {status}; no retry or fallback was attempted")
            decoded = json.loads(raw, parse_float=Decimal) if raw else None
            expect(not raw or isinstance(decoded, dict), f"{label} returned non-object service metadata")
            return status, decoded
        except subprocess.TimeoutExpired:
            record["timedOut"] = True
            raise RuntimeError(f"{label} absolute HTTP deadline expired; remote outcome is unknown; no retry") from None
        finally:
            record["finishedAt"] = datetime.now(timezone.utc).isoformat()
            record["durationSeconds"] = round(time.monotonic() - started, 6)

    def delete_owned_eval(self, eval_id, endpoint, tenant, *, cleanup_deadline=None):
        """Use the client's exact ID-only v1 route, never the CLI's name fallback."""
        require(isinstance(eval_id, str) and re.fullmatch(r"[A-Za-z0-9_-]+", eval_id),
                "Cleanup requires the exact returned evaluation ID")
        self.request("delete only owned evaluation and runs", "DELETE", endpoint,
                     "/openai/v1/evals/" + urllib.parse.quote(eval_id, safe=""), tenant,
                     accepted=(200, 204, 404), read_json=False,
                     cleanup_deadline=cleanup_deadline or time.monotonic() + self.timeout)
        return {"id": eval_id, "status": "deleted"}


def verify_identity(plan, driver):
    auth = driver("verify existing service identity", ["auth", "status"])
    require(isinstance(auth, dict) and auth.get("status") == "authenticated"
            and auth.get("type") == "servicePrincipal" and isinstance(auth.get("clientId"), str)
            and auth["clientId"].lower() == plan["clientId"].lower(),
            "An already authenticated matching service principal is required; no user login is accepted")
    identity = driver("verify native service token identity", [
        "auth", "token", "--scope", "https://ai.azure.com/.default",
    ], private_output=True)
    verify_native_identity_token(identity, plan)
    del identity
    for extension, command in scenario.EXTENSIONS.items():
        version = driver("verify approved " + extension, ["ai", command, "version"])
        require(version == {"name": extension, "version": plan["versions"][extension]},
                "Runtime extension version differs from the approved plan")


def approved_row(raw, digest, *, prompt=False):
    require(scenario.sha256(raw) == digest, "Input dataset bytes differ from the approved one-row content")
    documents = [json.loads(line, parse_float=Decimal, object_pairs_hook=unique_plan_object)
                 for line in raw.decode("utf-8-sig").splitlines() if line.strip()]
    require(len(documents) == 1 and isinstance(documents[0], dict)
            and isinstance(documents[0].get("query"), str), "Exactly one manual query row is required")
    if prompt:
        require(set(documents[0]) == {"query", "ground_truth"}
                and all(isinstance(value, str) and value.strip() for value in documents[0].values()),
                "Owned prompt evaluation requires one plain-text query/ground_truth row")
    else:
        require(isinstance(documents[0].get("response"), str),
                "Only one completed static query/response row is supported")
    return documents[0]


def begin_cleanup(plan, state):
    if "deadline" not in state:
        state["deadline"] = time.monotonic() + plan["timeoutSeconds"]
    return state["deadline"]


def lifecycle(plan, driver, workspace, report, name=None, *, target=None, identity_verified=False,
              cleanup_state=None):
    """Evaluate one registered row; target is supplied only by the owned prompt path."""
    name = name or f"{plan['ownerPrefix']}-{uuid.uuid4().hex}"
    endpoint = ["--project-endpoint", plan["projectEndpoint"]]
    cleanup_state = {} if cleanup_state is None else cleanup_state
    if not identity_verified:
        verify_identity(plan, driver)
    rows = workspace / "approved-row.jsonl"
    driver("download approved registered version", [
        "ai", "dataset", "download", plan["datasetName"], "--version", plan["datasetVersion"],
        "--output-file", str(rows), *endpoint,
    ])
    require(rows.is_file(),
            "Registered dataset bytes differ from the approved one-row content")
    document = approved_row(rows.read_bytes(), plan["datasetSha256"], prompt=target is not None)
    config = workspace / "azure.eval.yaml"
    evaluation = {"name": name, "dataset": plan["datasetName"], "evaluation_level": "turn",
                  "evaluators": [{"evaluator": plan["evaluator"],
                                  "initialization_parameters": {"model": plan["judgeModel"]}}]}
    if target is not None:
        evaluation["target"] = {"type": "agent", "name": target}
    scenario.write_json(config, {
        "datasets": [{"name": plan["datasetName"], "version": plan["datasetVersion"]}],
        "evals": [evaluation],
    })
    eval_id = None
    submitted = False
    report["ownedEvalName"] = name
    report["remoteCleanup"] = {"status": "NOT RUN"}
    body_completed = False
    try:
        submitted = True
        created = driver("create owned evaluation", [
            "ai", "eval", "create", name, "--from-file", str(config), *endpoint,
        ])
        expect(isinstance(created, dict) and created.get("name") == name
                and isinstance(created.get("id"), str) and re.fullmatch(r"[A-Za-z0-9_-]+", created["id"]),
                "Create did not return the owned evaluation identity")
        eval_id = created["id"]
        report["ownedEvalId"] = eval_id
        started = driver("start one authorized run", [
            "ai", "eval", "run", "start", "--eval", name, "--path", str(config),
            "--name", name + "-run", "--no-wait", *endpoint,
        ])
        expect(isinstance(started, dict) and started.get("eval_id") == eval_id
                and isinstance(started.get("run_id"), str)
                and re.fullmatch(r"[A-Za-z0-9_-]+", started["run_id"]),
                "Start did not return a run belonging to the owned evaluation")
        run_id = started["run_id"]
        report["ownedRunId"] = run_id
        final = driver("wait for owned run", [
            "ai", "eval", "run", "show", run_id, "--eval", eval_id, "--wait", *endpoint,
        ])
        expect(isinstance(final, dict) and final.get("id") == run_id and final.get("status") == "completed",
                "The run is not a completed execution; no quality success is inferred")
        exported = driver("export owned run", [
            "ai", "eval", "run", "output", "export", run_id, "--eval", eval_id,
            "--format", "json", "--output-file", "-", *endpoint,
        ])
        exported_run = exported.get("run") if isinstance(exported, dict) else None
        expect(isinstance(exported_run, dict) and exported_run.get("id") == run_id
                and isinstance(exported.get("items"), list) and len(exported["items"]) == 1,
                "Export did not contain the one approved run and row")
        item = exported["items"][0]
        expect(isinstance(item, dict) and item.get("run_id") == run_id
               and same_json_value(item.get("datasource_item"), document),
               "Exported item does not match the owned run and approved dataset row")
        counts = final.get("result_counts", {})
        expect(isinstance(counts, dict)
               and all(type(counts.get(key, 0)) is int for key in ("total", "passed", "failed", "errored", "skipped")),
               "Service result counts must be JSON integers, not booleans or coerced values")
        expect(counts.get("total") == 1 and counts.get("passed") == 1
                and counts.get("failed", 0) == 0 and counts.get("errored", 0) == 0
                and counts.get("skipped", 0) == 0, "The completed run did not pass the one-row quality assertion")
        report["quality"] = "PASS"
        body_completed = True
    finally:
        error = None if body_completed else sys.exception()
        if error is not None:
            report["failure"] = {"type": type(error).__name__,
                                 "message": scenario.proof_module.sanitize(str(error), workspace)}
        report["remoteBillingStopped"] = "NOT VERIFIED; timeouts and deletion are not monetary controls"
        if eval_id:
            try:
                deleted = driver.delete_owned_eval(
                    eval_id, plan["projectEndpoint"], plan["tenantId"],
                    cleanup_deadline=begin_cleanup(plan, cleanup_state))
                expect(isinstance(deleted, dict) and deleted.get("id") == eval_id
                        and deleted.get("status") == "deleted", "Owned eval deletion was not confirmed")
                report["remoteCleanup"] = {"status": "PASS", "evalId": eval_id}
            except (Blocked, RuntimeError, KeyError, ValueError, OSError) as error:
                report["remoteCleanup"] = {
                    "status": "FAIL", "message": scenario.proof_module.sanitize(str(error), workspace),
                }
                raise
        elif submitted:
            report["remoteCleanup"] = {
                "status": "BLOCKED", "manualReconciliationRequired": True,
                "reason": "Create outcome is ambiguous; do not repeat the POST or delete a guessed identity",
            }


def owned_prompt_lifecycle(plan, driver, workspace, report, raw_row):
    suffix = uuid.uuid4().hex
    agent_name = f"{plan['ownerPrefix']}-agent-{suffix}"
    dataset_name = f"{plan['ownerPrefix']}-data-{suffix}"
    eval_name = f"{plan['ownerPrefix']}-eval-{suffix}"
    endpoint, tenant = plan["projectEndpoint"], plan["tenantId"]
    endpoint_flags = ["--project-endpoint", endpoint]
    agent = dataset = None
    agent_attempted = dataset_attempted = False
    cleanup_state = {}
    report["ownedAgentName"], report["ownedDatasetName"] = agent_name, dataset_name
    report["agentCleanup"], report["datasetCleanup"] = {"status": "NOT RUN"}, {"status": "NOT RUN"}
    report["agentDeployment"] = "No hosted/code/voice infrastructure; prompt version registration only"
    report["agentTargetBinding"] = "Unique owned agent name; published eval config has no target.version field"
    report["datasetStorageRetention"] = "NOT VERIFIED; dataset-version deletion is not blob retention enforcement"
    report["agentContainerRetention"] = "Only the returned version is deleted; no whole-agent deletion is attempted"
    report["remoteBillingStopped"] = "NOT VERIFIED; timeouts and deletion are not monetary controls"
    body_completed = False
    try:
        verify_identity(plan, driver)
        agent_path = "/agents/" + urllib.parse.quote(agent_name, safe="")
        status, _ = driver.request("check new agent name is absent", "GET", endpoint,
                                   agent_path + "?api-version=v1", tenant, accepted=(200, 404))
        require(status == 404, "The generated agent name already exists; no existing agent will be adopted")
        definition = {"kind": "prompt", "model": plan["agentModel"], "instructions": plan["agentInstructions"]}
        agent_attempted = True
        _, created = driver.request(
            "create owned prompt-agent version", "POST", endpoint,
            agent_path + "/versions?api-version=v1", tenant,
            body={"definition": definition, "draft": False}, accepted=(200, 201))
        expect(isinstance(created, dict) and created.get("name") == agent_name
               and isinstance(created.get("version"), str)
               and re.fullmatch(r"[A-Za-z0-9][A-Za-z0-9._-]{0,100}", created["version"])
               and isinstance(created.get("id"), str) and created["id"].strip(),
               "Agent creation did not return the owned name/version identity")
        agent = {key: created[key] for key in ("id", "name", "version")}
        report["ownedAgentVersion"] = agent
        returned_definition = created.get("definition")
        expect(isinstance(returned_definition, dict)
               and all(returned_definition.get(key) == value for key, value in definition.items())
               and (returned_definition.get("tools") is None or returned_definition.get("tools") == [])
               and created.get("draft", False) is False,
               "Created agent differs from the approved minimal prompt definition")
        local_row = workspace / "manual-input.jsonl"
        local_row.write_bytes(raw_row)
        dataset_attempted = True
        registered = driver("create owned manual dataset version", [
            "ai", "dataset", "create", dataset_name, "--from-file", str(local_row),
            "--version", plan["datasetVersion"], *endpoint_flags,
        ])
        expect(isinstance(registered, dict) and registered.get("name") == dataset_name
               and registered.get("version") == plan["datasetVersion"],
               "Dataset creation did not return the owned name/version identity")
        dataset = {"name": registered["name"], "version": registered["version"]}
        report["ownedDatasetVersion"] = dataset
        lifecycle({**plan, "datasetName": dataset_name}, driver, workspace, report, name=eval_name,
                  target=agent_name, identity_verified=True, cleanup_state=cleanup_state)
        body_completed = True
    finally:
        primary = None if body_completed else sys.exception()
        if primary is not None and "failure" not in report:
            report["failure"] = {"type": type(primary).__name__,
                                 "message": scenario.proof_module.sanitize(str(primary), workspace)}
        cleanup_errors = []
        if dataset is not None:
            try:
                deleted = driver("delete only owned dataset version", [
                    "ai", "dataset", "delete", dataset["name"], "--version", dataset["version"],
                    "--force", *endpoint_flags,
                ], cleanup_deadline=begin_cleanup(plan, cleanup_state))
                expect(deleted == {**dataset, "status": "deleted"}, "Owned dataset-version deletion was not confirmed")
                report["datasetCleanup"] = {"status": "PASS", **dataset}
            except (Blocked, RuntimeError, KeyError, ValueError, OSError) as error:
                report["datasetCleanup"] = {"status": "FAIL", "message": scenario.safe_text(error)}
                cleanup_errors.append(error)
        elif dataset_attempted:
            report["datasetCleanup"] = {"status": "BLOCKED", "manualReconciliationRequired": True,
                                        "reason": "Ambiguous dataset create; do not retry or delete a guessed resource"}
        if agent is not None:
            try:
                _, deleted = driver.request(
                    "delete only owned prompt-agent version", "DELETE", endpoint,
                    "/agents/" + urllib.parse.quote(agent["name"], safe="") + "/versions/"
                    + urllib.parse.quote(agent["version"], safe="") + "?api-version=v1",
                    tenant, accepted=(200, 204, 404), cleanup_deadline=begin_cleanup(plan, cleanup_state))
                expect(deleted is None or (isinstance(deleted, dict) and deleted.get("deleted") is True
                       and deleted.get("name") == agent["name"] and deleted.get("version") == agent["version"]),
                       "Owned prompt-agent version deletion was not confirmed")
                report["agentCleanup"] = {"status": "PASS", **agent}
            except (Blocked, RuntimeError, KeyError, ValueError, OSError) as error:
                report["agentCleanup"] = {"status": "FAIL", "message": scenario.safe_text(error)}
                cleanup_errors.append(error)
        elif agent_attempted:
            report["agentCleanup"] = {"status": "BLOCKED", "manualReconciliationRequired": True,
                                      "reason": "Ambiguous version POST; do not retry or delete a guessed identity"}
        if cleanup_errors:
            raise RuntimeError("Owned resource cleanup failed; retained per-resource outcomes") from cleanup_errors[0]


def execute(plan_path, output, env=None):
    env = os.environ if env is None else env
    require(not output.exists(), "Output directory must be new")
    output.mkdir(parents=True)
    report = {"status": "BLOCKED", "execution": "NOT RUN", "remoteCleanup": {"status": "NOT RUN"},
              "datasetCleanup": {"status": "NOT RUN"}, "agentCleanup": {"status": "NOT RUN"},
              "implemented": ["static evaluation lifecycle", "owned prompt version/manual dataset lifecycle"],
              "executorImplemented": {
                  "staticWorkflow": True, "agentVersionCreate": True, "agentInfrastructureDeploy": False,
                  "datasetCreate": True, "evalCreate": True, "runWait": True, "export": True, "ownedCleanup": True,
              },
              "notImplemented": ["agent infrastructure deployment", "generation",
                                 "provider authentication/bootstrap", "service-side budget enforcement"]}
    workspace_state = {}
    try:
        raw = plan_path.read_bytes()
        plan = json.loads(raw, object_pairs_hook=unique_plan_object)
        report["planSha256"] = scenario.sha256(raw)
        report.update(validate_plan(plan, report["planSha256"], env))
        raw_row = None
        if plan["mode"] == "owned-prompt-evaluation":
            dataset_file = Path(plan["datasetFile"])
            require(dataset_file.is_file(), "Manual dataset input must be an existing regular file")
            raw_row = dataset_file.read_bytes()
            require(not raw_row.startswith(b"\xef\xbb\xbf"), "Manual upload must be UTF-8 without a byte-order mark")
            approved_row(raw_row, plan["datasetSha256"], prompt=True)
        config = Path(env.get("AZD_SCENARIO_LIVE_AUTH_CONFIG", "")).resolve()
        require(env.get("AZD_SCENARIO_LIVE_AUTH_CONFIG") and config.is_dir(),
                "An existing isolated CI service-auth configuration must be provided; never copy devbox caches")
        executable = verify_install(plan, config)
        report["status"], report["execution"] = "FAIL", "STARTED"
        with scenario.owned_workspace(workspace_state) as workspace:
            expires = datetime.fromisoformat(plan["expiresAt"].replace("Z", "+00:00"))
            duration = min(plan["durationSeconds"],
                           (expires - datetime.now(timezone.utc)).total_seconds() - plan["timeoutSeconds"])
            require(duration > 0, "Approval expired before execution and its reserved cleanup window")
            driver = Driver(executable, config, workspace, plan["timeoutSeconds"], duration, report)
            if raw_row is None:
                lifecycle(plan, driver, workspace, report)
            else:
                owned_prompt_lifecycle(plan, driver, workspace, report, raw_row)
        report["status"], report["execution"] = "PASS", "COMPLETED"
    except (Blocked, RuntimeError, KeyError, ValueError, OSError, InvalidOperation) as error:
        before_execution = report["execution"] == "NOT RUN"
        report["status"] = "BLOCKED" if isinstance(error, Blocked) and before_execution else "FAIL"
        report["error"] = {"type": type(error).__name__, "message": scenario.safe_text(error)}
        if isinstance(error, Blocked) and not before_execution:
            raise RuntimeError(str(error)) from error
        raise
    finally:
        report["cleanup"] = workspace_state.get("cleanup", {"status": "NOT RUN"})
        if workspace_state.get("failure") and "failure" not in report:
            report["failure"] = workspace_state["failure"]
        scenario.write_json(output / "service-status.json", report)
    return report


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--plan", required=True, type=Path)
    parser.add_argument("--output", required=True, type=Path)
    args = parser.parse_args()
    try:
        execute(args.plan, args.output)
    except Blocked as error:
        print(f"BLOCKED: {scenario.safe_text(error)}", file=sys.stderr)
        return 3
    except (RuntimeError, KeyError, ValueError, OSError, InvalidOperation) as error:
        print(f"FAIL: {scenario.safe_text(error)}", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
