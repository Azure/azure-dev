#!/usr/bin/env python3
# Copyright (c) Microsoft Corporation. All rights reserved.
# Licensed under the MIT License.

"""Opt-in static evaluation lifecycle using pre-authorized, existing resources.

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
import time
import urllib.parse
import urllib.error
import urllib.request
import uuid

import scenario


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
    allowed = {
        "schemaVersion", "mode", "provider", "workflowCommit", "runId", "expiresAt", "authorizedOperations",
        "approvalReference", "resourceOwner", "budgetControlReference", "budgetControlExternallyVerified",
        "approvedBudget", "maxRuns", "maxRows", "timeoutSeconds", "clientId", "tenantId", "datasetName",
        "datasetVersion", "datasetSha256", "evaluator", "judgeModel", "ownerPrefix", "projectEndpoint",
        "azdExecutable", "binarySha256", "versions",
        "durationSeconds", "ciIdentity",
    }
    require(isinstance(plan, dict) and set(plan) == allowed,
            "Plan contains unknown fields or omits required fields")
    for key in ("mode", "provider", "workflowCommit", "runId", "expiresAt", "approvalReference",
                "resourceOwner", "budgetControlReference", "clientId", "tenantId", "projectEndpoint",
                "azdExecutable"):
        require(isinstance(plan[key], str) and plan[key], f"Invalid {key}")
    require(env.get("AZD_SCENARIO_LIVE_APPROVAL_SHA256") == digest,
            "The exact plan lacks an externally supplied approval digest")
    require(type(plan.get("schemaVersion")) is int and plan["schemaVersion"] == 1
            and plan.get("mode") == "static-evaluation",
            "Only the reviewed static-evaluation lifecycle is implemented")
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
    require(isinstance(plan["authorizedOperations"], list)
            and all(isinstance(operation, str) for operation in plan["authorizedOperations"])
            and len(plan["authorizedOperations"]) == 5 and set(plan["authorizedOperations"]) == {
        "dataset-download", "eval-create", "eval-run", "eval-export", "eval-delete",
    }, "The exact lifecycle and its cleanup must be authorized")
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
    for key in ("datasetName", "datasetVersion", "evaluator", "judgeModel", "ownerPrefix"):
        require(isinstance(plan[key], str) and re.fullmatch(r"[A-Za-z0-9][A-Za-z0-9._/-]{0,100}", plan[key]),
                f"Invalid {key}")
    require(re.fullmatch(r"ci-[a-z0-9-]{1,20}", plan["ownerPrefix"]), "Owned names require a short ci- prefix")
    require(plan["evaluator"].startswith("builtin."), "Custom evaluator publication is not implemented")
    require(isinstance(plan["versions"], dict) and set(plan["versions"]) == set(scenario.EXTENSIONS)
            and all(isinstance(version, str) and version for version in plan["versions"].values()),
            "Both approved extension versions are required")
    require(scenario.HEX.fullmatch(plan["datasetSha256"]), "An approved immutable dataset-content digest is required")
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
            raise RuntimeError("Observation deadline elapsed; remote completion and billing are unknown")
        safe_args = [scenario.proof_module.sanitize(arg, self.workspace) for arg in ["azd", *argv[1:]]]
        if safe_args[1:4] == ["ai", "dataset", "download"]:
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

    def delete_owned_eval(self, eval_id, endpoint, tenant):
        """Use the client's exact ID-only v1 route, never the CLI's name fallback."""
        require(isinstance(eval_id, str) and re.fullmatch(r"[A-Za-z0-9_-]+", eval_id),
                "Cleanup requires the exact returned evaluation ID")
        deadline = time.monotonic() + self.timeout
        credential = self("acquire cleanup service token", [
            "auth", "token", "--scope", "https://ai.azure.com/.default", "--tenant-id", tenant,
        ], private_output=True, cleanup_deadline=deadline)
        require(isinstance(credential, dict) and isinstance(credential.get("token"), str)
                and credential["token"] and not any(char.isspace() for char in credential["token"]),
                "Native CI identity did not provide a usable cleanup token")
        request = urllib.request.Request(
            endpoint + "/openai/v1/evals/" + urllib.parse.quote(eval_id, safe=""),
            method="DELETE", headers={"Authorization": "Bearer " + credential["token"]},
        )

        class NoRedirect(urllib.request.HTTPRedirectHandler):
            def redirect_request(self, req, fp, code, msg, headers, newurl):
                return None

        timeout = deadline - time.monotonic()
        require(timeout > 0, "Owned cleanup command budget expired")
        record = {"name": "delete only owned evaluation and runs", "method": "DELETE",
                  "target": "<approved-project>/openai/v1/evals/<owned-id>",
                  "startedAt": datetime.now(timezone.utc).isoformat(), "timeoutSeconds": timeout}
        self.report.setdefault("commands", []).append(record)
        started = time.monotonic()
        try:
            try:
                with urllib.request.build_opener(NoRedirect()).open(request, timeout=timeout) as response:
                    status = response.status
            except urllib.error.HTTPError as error:
                status = error.code
                error.close()
            record["httpStatus"] = status
            expect(status in (200, 204, 404), "Owned ID deletion was not completed or confirmed absent")
            return {"id": eval_id, "status": "deleted"}
        except (urllib.error.URLError, TimeoutError, OSError):
            raise RuntimeError("Owned ID cleanup request failed; no retry or name fallback was attempted") from None
        finally:
            record["finishedAt"] = datetime.now(timezone.utc).isoformat()
            record["durationSeconds"] = round(time.monotonic() - started, 6)


def lifecycle(plan, driver, workspace, report, name=None):
    """Run one static eval over one existing immutable dataset, then remove only that eval."""
    name = name or f"{plan['ownerPrefix']}-{uuid.uuid4().hex}"
    endpoint = ["--project-endpoint", plan["projectEndpoint"]]
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
    rows = workspace / "approved-row.jsonl"
    driver("download approved registered version", [
        "ai", "dataset", "download", plan["datasetName"], "--version", plan["datasetVersion"],
        "--output-file", str(rows), *endpoint,
    ])
    require(rows.is_file() and scenario.sha256(rows.read_bytes()) == plan["datasetSha256"],
            "Registered dataset bytes differ from the approved one-row content")
    documents = [json.loads(line, parse_float=Decimal) for line in rows.read_text(encoding="utf-8-sig").splitlines()
                 if line.strip()]
    require(len(documents) == 1 and isinstance(documents[0], dict)
            and isinstance(documents[0].get("query"), str) and isinstance(documents[0].get("response"), str),
            "Only one completed static query/response row is supported")
    config = workspace / "azure.eval.yaml"
    scenario.write_json(config, {
        "datasets": [{"name": plan["datasetName"], "version": plan["datasetVersion"]}],
        "evals": [{"name": name, "dataset": plan["datasetName"], "evaluation_level": "turn",
                   "evaluators": [{"evaluator": plan["evaluator"],
                                   "initialization_parameters": {"model": plan["judgeModel"]}}]}],
    })
    # No dataset file, target, source or simulation block: never publish input
    # artifacts, invoke an agent, generate content, or adopt a caller-supplied eval.
    eval_id = None
    submitted = False
    report["ownedEvalName"] = name
    report["remoteCleanup"] = {"status": "NOT RUN"}
    body_completed = False
    try:
        submitted = True
        created = driver("create owned static evaluation", [
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
        expect(isinstance(exported, dict) and exported.get("run", {}).get("id") == run_id
                and isinstance(exported.get("items"), list) and len(exported["items"]) == 1,
                "Export did not contain the one approved run and row")
        item = exported["items"][0]
        expect(isinstance(item, dict) and item.get("run_id") == run_id
               and same_json_value(item.get("datasource_item"), documents[0]),
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
                deleted = driver.delete_owned_eval(eval_id, plan["projectEndpoint"], plan["tenantId"])
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


def execute(plan_path, output, env=None):
    env = os.environ if env is None else env
    require(not output.exists(), "Output directory must be new")
    output.mkdir(parents=True)
    report = {"status": "BLOCKED", "execution": "NOT RUN", "remoteCleanup": {"status": "NOT RUN"},
              "implemented": "static evaluation lifecycle",
              "notImplemented": ["agent-create", "agent-deploy", "dataset-create", "generation",
                                 "provider authentication/bootstrap", "service-side budget enforcement"]}
    workspace_state = {}
    try:
        raw = plan_path.read_bytes()
        plan = json.loads(raw, object_pairs_hook=unique_plan_object)
        report["planSha256"] = scenario.sha256(raw)
        report.update(validate_plan(plan, report["planSha256"], env))
        config = Path(env.get("AZD_SCENARIO_LIVE_AUTH_CONFIG", "")).resolve()
        require(env.get("AZD_SCENARIO_LIVE_AUTH_CONFIG") and config.is_dir(),
                "An existing isolated CI service-auth configuration must be provided; never copy devbox caches")
        executable = verify_install(plan, config)
        report["status"], report["execution"] = "FAIL", "STARTED"
        with scenario.owned_workspace(workspace_state) as workspace:
            expires = datetime.fromisoformat(plan["expiresAt"].replace("Z", "+00:00"))
            duration = min(plan["durationSeconds"], (expires - datetime.now(timezone.utc)).total_seconds())
            driver = Driver(executable, config, workspace, plan["timeoutSeconds"], duration, report)
            lifecycle(plan, driver, workspace, report)
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
