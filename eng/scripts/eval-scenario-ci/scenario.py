#!/usr/bin/env python3
# Copyright (c) Microsoft Corporation. All rights reserved.
# Licensed under the MIT License.

"""Resolve Latest once, then exercise its immutable installed offline CLI."""

import argparse
import copy
from datetime import datetime, timezone
import importlib.util
import json
import os
from pathlib import Path
import re
import subprocess
import sys
import urllib.error
import urllib.parse
import urllib.request


HERE = Path(__file__).resolve().parent
BASELINE = HERE.parent / "eval-candidate-proof"
FEED = "m7md7sien/azd-foundry-feed"
PLATFORMS = ("linux/amd64", "windows/amd64")
EXTENSIONS = {"azure.ai.evaluations": "eval", "azure.ai.dataset": "dataset"}
HEX = re.compile(r"^[0-9a-f]{64}$")
TAG = re.compile(r"^extensions-\d{4}-\d{2}-\d{2}-[1-9]\d*$")
CANCEL_ARITY_ERROR = r"^accepts at most 1 arg\(s\), received 2$"

spec = importlib.util.spec_from_file_location("candidate_proof", BASELINE / "verify.py")
proof_module = importlib.util.module_from_spec(spec)
spec.loader.exec_module(proof_module)
require = proof_module.require
sha256 = proof_module.sha256
write_json = proof_module.write_json
cleanup_owned_workspace = proof_module.cleanup_owned_workspace
owned_workspace = proof_module.owned_workspace


class ApprovalBlocked(AssertionError):
    pass


def require_approval(condition, message):
    if not condition:
        raise ApprovalBlocked(message)


def validate_approval_manifest(approved):
    fields = {"releaseRepository", "releaseTag", "sourceCommit", "sourceVerificationCommit", "validationBaseline",
              "conversationModes", "initSeedValidation", "initDatasetBinding", "registrySha256", "azd", "extensions"}
    require_approval(isinstance(approved, dict) and fields <= set(approved)
                     and set(approved) <= fields | {"sourceNote"}, "Approval manifest has missing or unsupported fields")
    require_approval(approved["releaseRepository"] == FEED
                     and isinstance(approved["releaseTag"], str) and TAG.fullmatch(approved["releaseTag"])
                     and isinstance(approved["registrySha256"], str) and HEX.fullmatch(approved["registrySha256"]),
                     "Approval release identity is malformed")
    require_approval(all(isinstance(approved[key], str) and re.fullmatch(r"[0-9a-f]{40}", approved[key])
                         for key in ("sourceCommit", "sourceVerificationCommit", "validationBaseline"))
                     and approved["sourceCommit"] == approved["sourceVerificationCommit"],
                     "Approval source identities are malformed")
    require_approval(all(approved[key] is True
                         for key in ("conversationModes", "initSeedValidation", "initDatasetBinding")),
                     "Approval does not retain the canonical160 fixture contract")
    core = approved["azd"]
    require_approval(isinstance(core, dict) and set(core) == {"version", "artifacts"}
                     and isinstance(core["version"], str) and re.fullmatch(r"\d+\.\d+\.\d+", core["version"])
                     and isinstance(core["artifacts"], dict) and set(core["artifacts"]) == set(PLATFORMS),
                     "Approval core execution fields are malformed")
    for artifact in core["artifacts"].values():
        require_approval(isinstance(artifact, dict) and set(artifact) == {"file", "sha256"}
                         and isinstance(artifact["file"], str) and artifact["file"] not in (".", "..")
                         and re.fullmatch(r"[A-Za-z0-9._-]+", artifact["file"])
                         and isinstance(artifact["sha256"], str) and HEX.fullmatch(artifact["sha256"]),
                         "Approval core artifact is malformed")
    entries = approved["extensions"]
    require_approval(isinstance(entries, dict) and set(entries) == set(EXTENSIONS),
                     "Approval extension identities are malformed")
    for extension, command in EXTENSIONS.items():
        entry = entries[extension]
        require_approval(isinstance(entry, dict) and set(entry) == {"command", "version", "artifacts"}
                         and entry["command"] == command and isinstance(entry["version"], str)
                         and re.fullmatch(r"\d+\.\d+\.\d+-beta(?:\.\d+)?", entry["version"])
                         and isinstance(entry["artifacts"], dict) and set(entry["artifacts"]) == set(PLATFORMS)
                         and all(isinstance(value, str) and HEX.fullmatch(value)
                                 for value in entry["artifacts"].values()),
                         "Approval extension execution fields are malformed")


def reviewed_candidate(env=None):
    env = os.environ if env is None else env
    repository = env.get("AZD_SCENARIO_APPROVAL_REPOSITORY", "")
    revision = env.get("AZD_SCENARIO_APPROVED_COMMIT", "")
    parts = repository.split("/")
    require_approval(len(parts) == 2
                     and all(part not in (".", "..") and re.fullmatch(r"[A-Za-z0-9_.-]+", part) for part in parts)
                     and re.fullmatch(r"[0-9a-f]{40}", revision),
                     "An independently configured immutable repository approval revision is required")
    path = "eng/scripts/eval-candidate-proof/candidate.json"
    try:
        raw = fetch(f"https://github.com/{repository}/raw/{revision}/{path}")
        approved = json.loads(raw.decode("utf-8-sig"))
    except (ValueError, RuntimeError) as error:
        raise ApprovalBlocked("Configured immutable approval could not be retrieved or parsed") from error
    validate_approval_manifest(approved)
    return approved, {"repository": repository, "commit": revision, "path": path, "sha256": sha256(raw)}


def require_reviewed_candidate(pin, approved, authority):
    # Publisher hashes prove consistency, not authorization to execute new bytes.
    require_approval(isinstance(pin, dict) and isinstance(approved, dict)
                     and set(pin) <= set(approved) | {"scenarioResolution"},
                     "Manifest is outside the repository-reviewed candidate contract")
    for key, value in approved.items():
        if key != "sourceNote":
            require_approval(pin.get(key) == value,
                             f"Candidate {key} differs from repository-reviewed immutable pins; no binary may execute")
    resolution = pin.get("scenarioResolution", {})
    require_approval(isinstance(resolution, dict), "Producer resolution metadata must be an object")
    claimed = resolution.get("approval")
    require_approval(claimed == authority,
                     "Producer approval claim differs from independent configuration")


def record_approval_block(directory, error):
    directory.mkdir(parents=True, exist_ok=True)
    status = directory / "approval-status.json"
    require(not status.exists(), "Refusing to overwrite approval evidence")
    write_json(status, {"status": "BLOCKED", "execution": "NOT RUN", "reason": safe_text(error)})


def safe_text(value):
    return proof_module.sanitize(str(value), HERE)


def fetch(url):
    parsed = urllib.parse.urlsplit(url)
    require(parsed.scheme == "https" and parsed.hostname in ("github.com", "api.github.com")
            and parsed.port is None and not parsed.username and not parsed.password
            and not parsed.query and not parsed.fragment, "Untrusted release URL")
    request = urllib.request.Request(url, headers={
        "Accept": "application/vnd.github+json" if parsed.hostname == "api.github.com" else "*/*",
        "User-Agent": "azd-installed-scenario-ci",
    })
    try:
        with urllib.request.urlopen(request, timeout=120) as response:
            return response.read()
    except (urllib.error.URLError, TimeoutError) as error:
        raise RuntimeError(f"Release download failed: {safe_text(error)}") from None


def asset_map(release, tag):
    result = {}
    prefix = f"https://github.com/{FEED}/releases/download/{tag}/"
    for asset in release["assets"]:
        name = asset["name"]
        require(name not in (".", "..") and re.fullmatch(r"[A-Za-z0-9._-]+", name), "Unsafe asset filename")
        require(name not in result, "Duplicate release asset")
        require(asset["browser_download_url"] == prefix + name, "Asset is outside frozen release")
        digest = asset.get("digest", "")
        require(isinstance(digest, str) and digest.startswith("sha256:")
                and HEX.fullmatch(digest[7:]), "Release API asset lacks SHA-256")
        result[name] = {"url": prefix + name, "sha256": digest[7:]}
    return result


def parse_sums(data):
    result = {}
    for line in data.decode("utf-8-sig").splitlines():
        fields = line.split()
        require(len(fields) == 2 and HEX.fullmatch(fields[0]), "Malformed SHA256SUMS")
        name = fields[1].removeprefix("*")
        require(name not in (".", "..") and re.fullmatch(r"[A-Za-z0-9._-]+", name) and name not in result,
                "Unsafe or duplicate checksum entry")
        result[name] = fields[0]
    return result


def build_manifest(release, assets, registry, provenance, sums, baseline, authority):
    tag = release["tag_name"]
    require(TAG.fullmatch(tag), "Unexpected bug-bash release tag")
    require(not release["draft"] and not release["prerelease"], "Latest must be a published release")
    require(provenance["releaseTag"] == tag, "Provenance release mismatch")
    require(provenance["sourceRepository"] == "https://github.com/m7md7sien/azure-dev",
            "Unexpected source repository")
    source = provenance["sourceCommit"]
    require(isinstance(source, str) and re.fullmatch(r"[0-9a-f]{40}", source),
            "Provenance must declare an immutable source SHA")
    require(provenance["azdVersion"] == baseline["azd"]["version"],
            "Latest requires a different core version; update reviewed core pins first")
    pin = copy.deepcopy(baseline)
    pin.update({
        "releaseRepository": FEED, "releaseTag": tag,
        "sourceCommit": source, "sourceVerificationCommit": source,
        "sourceNote": "Publisher provenance checked against repository-reviewed immutable candidate pins.",
        "registrySha256": assets["registry.json"]["sha256"],
    })
    entries = registry["extensions"]
    require(len(entries) == len(EXTENSIONS) and {entry["id"] for entry in entries} == set(EXTENSIONS),
            "Registry must contain exactly the evaluation and dataset extensions")
    frozen = {}
    for entry in entries:
        extension = entry["id"]
        require(len(entry["versions"]) == 1, "Candidate registry must contain one version per extension")
        version = entry["versions"][0]
        require(isinstance(version["version"], str)
                and re.fullmatch(r"\d+\.\d+\.\d+-beta(?:\.\d+)?", version["version"]),
                "Unexpected extension version")
        pin["extensions"][extension]["version"] = version["version"]
        frozen[extension] = {}
        for platform in PLATFORMS:
            artifact = version["artifacts"][platform]
            name = artifact["url"].rsplit("/", 1)[-1]
            require(name in assets and artifact["url"] == assets[name]["url"], "Unpinned archive URL")
            digest = assets[name]["sha256"]
            require(artifact["checksum"] == {"algorithm": "sha256", "value": digest}
                    and sums.get(name) == digest, "Archive digest sources disagree")
            records = [record for record in provenance["archives"]
                       if record["extension"] == extension and record["platform"] == platform]
            require(len(records) == 1, "Missing or duplicate archive provenance")
            record = records[0]
            require(record["name"] == name and record["version"] == version["version"]
                    and record["sha256"] == digest and record["entryPoint"] == artifact["entryPoint"],
                    "Archive provenance disagrees with registry")
            require(artifact["entryPoint"] not in (".", "..")
                    and re.fullmatch(r"[A-Za-z0-9._-]+", artifact["entryPoint"]), "Unsafe archive entry point")
            pin["extensions"][extension]["artifacts"][platform] = digest
            frozen[extension][platform] = {**assets[name], "entryPoint": artifact["entryPoint"]}
    for name in ("registry.json", "source-provenance.json"):
        require(sums.get(name) == assets[name]["sha256"], "Metadata checksum sources disagree")
    pin["scenarioResolution"] = {
        "schemaVersion": 1,
        "resolvedFrom": f"https://api.github.com/repos/{FEED}/releases/latest",
        "releaseId": release["id"],
        "publishedAt": release["published_at"],
        "resolvedAt": datetime.now(timezone.utc).isoformat(),
        "metadata": {name: assets[name] for name in
                     ("registry.json", "source-provenance.json", "SHA256SUMS")},
        "artifacts": frozen,
        "sourceEvidence": "Repository-reviewed immutable pins; publisher provenance is corroborating evidence only.",
        "fixtureContract": "build41-offline-160",
        "approval": authority,
    }
    require_reviewed_candidate(pin, baseline, authority)
    return pin


def resolve(output):
    require(not output.exists(), "Refusing to overwrite a frozen manifest")
    try:
        approved, authority = reviewed_candidate()
        release = json.loads(fetch(f"https://api.github.com/repos/{FEED}/releases/latest"))
        tag = release["tag_name"]
        require(TAG.fullmatch(tag), "Unexpected bug-bash release tag")
        require_approval(tag == approved["releaseTag"],
                         "Latest is not the repository-reviewed release; review immutable pins before execution")
        assets = asset_map(release, tag)
        require_approval(assets["registry.json"]["sha256"] == approved["registrySha256"],
                         "Latest registry digest differs from repository-reviewed pins")
        documents = {}
        for name in ("registry.json", "source-provenance.json", "SHA256SUMS"):
            data = fetch(assets[name]["url"])
            require(sha256(data) == assets[name]["sha256"], "Metadata differs from release API digest")
            documents[name] = data
        pin = build_manifest(release, assets, json.loads(documents["registry.json"].decode("utf-8-sig")),
                             json.loads(documents["source-provenance.json"].decode("utf-8-sig")),
                             parse_sums(documents["SHA256SUMS"]), approved, authority)
    except ApprovalBlocked as error:
        record_approval_block(output.parent, error)
        raise
    output.parent.mkdir(parents=True, exist_ok=True)
    write_json(output, pin)
    print(f"Resolved Latest once: {tag}, source {pin['sourceCommit']}", flush=True)
    print(f"Frozen manifest SHA256: {sha256(output.read_bytes())}", flush=True)


def run_identity(env):
    if env.get("GITHUB_RUN_ID"):
        return {"provider": "github", "runUrl": (
            f"https://github.com/{env['GITHUB_REPOSITORY']}/actions/runs/{env['GITHUB_RUN_ID']}"
        ), "workflowCommit": env["GITHUB_SHA"], "attempt": env.get("GITHUB_RUN_ATTEMPT", "1")}
    if env.get("BUILD_BUILDID"):
        base = safe_text(env["SYSTEM_COLLECTIONURI"]).rstrip("/")
        project = urllib.parse.quote(env["SYSTEM_TEAMPROJECT"], safe="")
        build_id = str(int(env["BUILD_BUILDID"]))
        return {"provider": "azure-devops", "runUrl": (
            f"{base}/{project}/_build/results?buildId={build_id}"
        ), "buildId": build_id, "workflowCommit": env["BUILD_SOURCEVERSION"],
                "attempt": env.get("SYSTEM_JOBATTEMPT", "1")}
    return {"provider": "local", "runUrl": None, "workflowCommit": None, "attempt": "1"}


def live_status():
    return {
        "status": "BLOCKED", "execution": "NOT RUN",
        "executorSelected": False,
        "implementedServiceModes": ["static-evaluation", "owned-prompt-evaluation"],
        "unimplementedServiceOperations": ["agent-infrastructure-deploy", "generation"],
        "operations": ["agent-create", "agent-deploy", "dataset-create",
                       "eval-create", "eval-run", "eval-export"],
        "blockers": [
            "No approved existing CI identity/service connection and resource tuple is configured.",
            "No explicit service-operation cost/budget authorization is configured.",
        ],
        "paidGeneration": "NOT AUTHORIZED", "deployment": "NOT AUTHORIZED",
        "interactiveCancellation": "NOT RUN; offline argument refusals are not PTY proof.",
    }


def github_live_gate(output, env=None):
    env = os.environ if env is None else env
    output.mkdir(parents=True, exist_ok=False)
    report = {"status": "BLOCKED", "execution": "NOT RUN",
              "scope": "Existing GitHub environment metadata only; not deployment approval"}
    try:
        plan = env.get("SERVICE_PLAN", "")
        name = env.get("AZD_SCENARIO_LIVE_ENVIRONMENT", "")
        repository = env.get("GITHUB_REPOSITORY", "")
        if not plan or not Path(plan).is_file():
            report["reason"] = "An available reviewed service plan is required"
            return 3
        if not name or any(char in name for char in "\r\n\x00"):
            report["reason"] = "An existing protected environment must be configured"
            return 3
        if (env.get("GITHUB_SERVER_URL") != "https://github.com"
                or not re.fullmatch(r"[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+", repository)
                or not env.get("GITHUB_OUTPUT") or not env.get("GH_TOKEN")):
            report["reason"] = "Supported native GitHub context and output binding are required"
            return 3
        result = subprocess.run([
            "gh", "api", "--hostname", "github.com", "--method", "GET",
            f"repos/{repository}/environments/{urllib.parse.quote(name, safe='')}",
            "--jq", "{name,requiredReviewers:([.protection_rules[]?"
            " | select(.type==\"required_reviewers\") | .reviewers | length] | add // 0)}",
        ], env=env, capture_output=True, text=True, timeout=60, check=False)
        if result.returncode != 0:
            report["reason"] = "Existing environment metadata was unavailable or access was denied"
            return 3
        metadata = json.loads(result.stdout)
        if (not isinstance(metadata, dict) or metadata.get("name") != name
                or type(metadata.get("requiredReviewers")) is not int
                or metadata["requiredReviewers"] <= 0):
            report["reason"] = "The existing environment must have required-reviewer protection"
            return 3
        with open(env["GITHUB_OUTPUT"], "a", encoding="utf-8") as stream:
            stream.write(f"environment_name={name}\n")
        report["status"] = "PASS"
        report["reason"] = "Existing reviewer-protected environment verified; native approval is still required"
        return 0
    except (OSError, ValueError, subprocess.SubprocessError):
        report["reason"] = "Protected environment metadata could not be verified"
        return 3
    finally:
        write_json(output / "environment-gate.json", report)
        print(f"{report['status']}: {report['reason']}", flush=True)


def installed_evidence(proof, pin):
    platform = proof.platform
    core = pin["azd"]["artifacts"][platform]
    core_archive = proof.root / "downloads" / core["file"]
    core_entry = "azd-windows-amd64.exe" if platform == "windows/amd64" else "azd-linux-amd64"
    expected_core = sha256(proof_module.binary_from_archive(core_archive, core_entry))
    installed_core = sha256(proof.azd.read_bytes())
    require(sha256(core_archive.read_bytes()) == core["sha256"] and installed_core == expected_core,
            "Installed core differs from its frozen archive")
    records = {"azd": {
        "version": pin["azd"]["version"], "archiveSha256": core["sha256"],
        "archiveUrl": (
            "https://github.com/Azure/azure-dev/releases/download/"
            f"azure-dev-cli_{pin['azd']['version']}/{core['file']}"
        ),
        "archiveBinarySha256": expected_core, "installedBinarySha256": installed_core,
    }}
    for extension in EXTENSIONS:
        asset = pin["scenarioResolution"]["artifacts"][extension][platform]
        archive = proof.root / "downloads" / asset["url"].rsplit("/", 1)[-1]
        installed = proof.root / "config" / "extensions" / extension / asset["entryPoint"]
        expected = sha256(proof_module.binary_from_archive(archive, asset["entryPoint"]))
        actual = sha256(installed.read_bytes())
        require(sha256(archive.read_bytes()) == asset["sha256"] and actual == expected,
                "Installed/archive byte comparison failed")
        records[extension] = {
            "version": pin["extensions"][extension]["version"], "archiveUrl": asset["url"],
            "archiveSha256": asset["sha256"], "archiveBinarySha256": expected,
            "installedBinarySha256": actual,
        }
    return records


def extra_scenarios(proof):
    primary = proof.env["AZD_CONFIG_DIR"]
    proof.run("isolated config write primary", ["config", "set", "scenario.marker", "primary"])
    require(proof.run("isolated config read primary", ["config", "get", "scenario.marker"],
                      json_output=True) == "primary", "Primary profile did not retain its value")
    before = (Path(primary) / "config.json").read_bytes()
    secondary = proof.root / "secondary-config"
    secondary.mkdir()
    try:
        proof.env["AZD_CONFIG_DIR"] = str(secondary)
        config = proof.run("fresh profile has no inherited configuration", ["config", "show"], json_output=True)
        require(config == {}, "Fresh profile inherited configuration")
        proof.run("isolated config write secondary", ["config", "set", "scenario.marker", "secondary"])
        require(proof.run("isolated config read secondary", ["config", "get", "scenario.marker"],
                          json_output=True) == "secondary", "Secondary profile did not retain its value")
    finally:
        proof.env["AZD_CONFIG_DIR"] = primary
    require((Path(primary) / "config.json").read_bytes() == before, "Secondary profile changed primary config")
    require(proof.run("primary config survives environment switch", ["config", "get", "scenario.marker"],
                      json_output=True) == "primary", "Profile switch lost primary configuration")
    project = proof.root / "synthetic-project"
    refusals = []
    for name, flags, error in (
        ("cancel refuses multiple run IDs", ["fixture-run", "another-run"],
         CANCEL_ARITY_ERROR),
        ("cancel refuses unknown flag", ["fixture-run", "--not-a-real-flag"], "unknown flag"),
    ):
        refusals.append(proof.refuse_without_writes(
            name, ["ai", "eval", "run", "cancel", *flags, "--output", "json"], project, error))
    write_json(proof.output / "scenario-state.json", {
        "primaryConfigurationPreserved": True,
        "primaryConfigurationSha256": sha256(before),
        "freshSecondaryConfigurationWasEmpty": True,
        "cancellationArgumentRefusals": refusals,
        "interactiveCancellation": "NOT RUN",
    })


def execute(manifest, output):
    require(not output.exists(), "Evidence directory must be new")
    pin_bytes = manifest.read_bytes()
    pin = json.loads(pin_bytes)
    try:
        approved, authority = reviewed_candidate()
        require_reviewed_candidate(pin, approved, authority)
    except ApprovalBlocked as error:
        record_approval_block(output, error)
        raise
    require(pin["scenarioResolution"]["fixtureContract"] == "build41-offline-160",
            "Unsupported offline fixture contract")
    require(pin["sourceCommit"] == pin["sourceVerificationCommit"], "Source pins disagree")
    output.mkdir(parents=True)
    (output / "candidate.json").write_bytes(pin_bytes)
    report = {
        **run_identity(os.environ), "status": "FAIL", "coverage": "actual installed offline CLI",
        "manifestSha256": sha256(pin_bytes), "releaseTag": pin["releaseTag"],
        "sourceCommit": pin["sourceCommit"], "live": live_status(),
        "baselineCheckCount": 0, "scenarioCheckCount": 0,
        "cleanup": {"status": "NOT RUN"},
        "approval": authority,
    }
    proof = None
    try:
        with owned_workspace(report) as directory:
            proof = proof_module.Proof(directory, output, pin)
            report["platform"] = proof.platform
            require(not any(key.startswith(("AZURE_CLIENT_", "AZURE_TENANT_", "GITHUB_TOKEN", "SYSTEM_ACCESSTOKEN"))
                            for key in proof.env), "Credential environment leaked into child")
            proof.install()
            report["installed"] = installed_evidence(proof, pin)
            proof.exercise()
            proof_module.validate_baseline_checks(proof.checks)
            report["baselineCheckCount"] = len(proof.checks)
            extra_scenarios(proof)
            report["scenarioCheckCount"] = len(proof.checks) - 160
            require(report["scenarioCheckCount"] == 8, "Additional scenario count changed")
        require(manifest.read_bytes() == pin_bytes, "Frozen manifest changed during execution")
        report["status"] = "PASS"
    finally:
        error = sys.exception()
        if report["status"] != "PASS" and error is not None and "failure" not in report:
            report["failure"] = {"type": type(error).__name__, "message": safe_text(error)}
        report["checks"] = proof.checks if proof else []
        write_json(output / "commands.json", proof.commands if proof else [])
        write_json(output / "results.json", report)
        write_json(output / "live-status.json", report["live"])
        summary = (
            f"# Installed offline scenarios: {report['status']}\n\n"
            f"- Release: `{pin['releaseTag']}`\n- Source: `{pin['sourceCommit']}`\n"
            f"- Platform: `{report.get('platform', 'not initialized')}`\n"
            f"- Baseline checks: {report['baselineCheckCount']}\n"
            f"- Additional checks: {report['scenarioCheckCount']}\n"
            f"- Cleanup: **{report['cleanup']['status']}**\n"
            f"- Manifest SHA256: `{report['manifestSha256']}`\n"
            f"- Run: {report['runUrl'] or 'local'}\n"
            "- Live agent/deployment/evaluation/export: **BLOCKED / NOT RUN**.\n"
            "- Interactive Ctrl+C/Cancel: **NOT RUN**; cancellation coverage is invalid-input refusal only.\n"
            "- No Azure login, resource creation, paid generation, or deployment was attempted.\n"
        )
        if report.get("failure"):
            summary += f"- Scenario failure: {report['failure']['message']}\n"
        if report["cleanup"].get("error"):
            summary += f"- Cleanup failure: {report['cleanup']['error']}\n"
        (output / "summary.md").write_text(summary, encoding="utf-8")
        if os.environ.get("GITHUB_STEP_SUMMARY"):
            with open(os.environ["GITHUB_STEP_SUMMARY"], "a", encoding="utf-8") as stream:
                stream.write(summary)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    commands = parser.add_subparsers(dest="operation", required=True)
    resolver = commands.add_parser("resolve")
    resolver.add_argument("--output", required=True, type=Path)
    runner = commands.add_parser("offline")
    runner.add_argument("--manifest", required=True, type=Path)
    runner.add_argument("--output", required=True, type=Path)
    live = commands.add_parser("live")
    live.add_argument("--output", required=True, type=Path)
    environment_gate = commands.add_parser("github-live-gate")
    environment_gate.add_argument("--output", required=True, type=Path)
    args = parser.parse_args()
    try:
        if args.operation == "resolve":
            resolve(args.output)
        elif args.operation == "offline":
            execute(args.manifest, args.output)
        elif args.operation == "github-live-gate":
            return github_live_gate(args.output)
        else:
            args.output.mkdir(parents=True, exist_ok=False)
            write_json(args.output / "live-status.json", live_status())
            print("BLOCKED / NOT RUN: live execution requires approved identity, resources and budget.", file=sys.stderr)
            return 3
    except ApprovalBlocked as error:
        print(f"BLOCKED / NOT RUN: {safe_text(error)}", file=sys.stderr)
        return 3
    except (AssertionError, KeyError, ValueError, OSError, RuntimeError, subprocess.SubprocessError) as error:
        print(f"Scenario CI failed: {safe_text(error)}", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
