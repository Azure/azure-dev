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


def build_manifest(release, assets, registry, provenance, sums, baseline):
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
        "sourceNote": "Publisher-declared source from checksum-verified release provenance.",
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
        "sourceEvidence": "Publisher provenance; installed bytes verified against these archives.",
        "fixtureContract": "build41-offline-160",
    }
    return pin


def resolve(output):
    require(not output.exists(), "Refusing to overwrite a frozen manifest")
    release = json.loads(fetch(f"https://api.github.com/repos/{FEED}/releases/latest"))
    tag = release["tag_name"]
    require(TAG.fullmatch(tag), "Unexpected bug-bash release tag")
    assets = asset_map(release, tag)
    documents = {}
    for name in ("registry.json", "source-provenance.json", "SHA256SUMS"):
        data = fetch(assets[name]["url"])
        require(sha256(data) == assets[name]["sha256"], "Metadata differs from release API digest")
        documents[name] = data
    pin = build_manifest(release, assets, json.loads(documents["registry.json"].decode("utf-8-sig")),
                         json.loads(documents["source-provenance.json"].decode("utf-8-sig")),
                         parse_sums(documents["SHA256SUMS"]),
                         json.loads((BASELINE / "candidate.json").read_text(encoding="utf-8")))
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
        "implementedServiceModes": ["static-evaluation"],
        "unimplementedServiceOperations": ["agent-create", "agent-deploy", "dataset-create", "generation"],
        "operations": ["agent-create", "agent-deploy", "dataset-create",
                       "eval-create", "eval-run", "eval-export"],
        "blockers": [
            "No approved existing CI identity/service connection and resource tuple is configured.",
            "No explicit service-operation cost/budget authorization is configured.",
        ],
        "paidGeneration": "NOT AUTHORIZED", "deployment": "NOT AUTHORIZED",
        "interactiveCancellation": "NOT RUN; offline argument refusals are not PTY proof.",
    }


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
    args = parser.parse_args()
    try:
        if args.operation == "resolve":
            resolve(args.output)
        elif args.operation == "offline":
            execute(args.manifest, args.output)
        else:
            args.output.mkdir(parents=True, exist_ok=False)
            write_json(args.output / "live-status.json", live_status())
            print("BLOCKED / NOT RUN: live execution requires approved identity, resources and budget.", file=sys.stderr)
            return 3
    except (AssertionError, KeyError, ValueError, OSError, RuntimeError, subprocess.SubprocessError) as error:
        print(f"Scenario CI failed: {safe_text(error)}", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
