# Copyright (c) Microsoft Corporation. All rights reserved.
# Licensed under the MIT License.

import copy
from datetime import datetime
import json
from pathlib import Path
import tempfile
import unittest
from unittest import mock

import scenario


class ResolutionTests(unittest.TestCase):
    def setUp(self):
        self.tag = "extensions-2026-09-23-41"
        self.base = f"https://github.com/{scenario.FEED}/releases/download/{self.tag}/"
        self.baseline = json.loads((scenario.BASELINE / "candidate.json").read_text())
        self.release = {"id": 123, "tag_name": self.tag, "draft": False, "prerelease": False,
                        "published_at": "2026-09-23T00:00:00Z", "assets": []}
        self.registry = {"extensions": []}
        self.provenance = {
            "releaseTag": self.tag, "sourceRepository": "https://github.com/m7md7sien/azure-dev",
            "sourceCommit": "a" * 40, "azdVersion": self.baseline["azd"]["version"], "archives": [],
        }
        self.assets = {}
        self.sums = {}
        for name in ("registry.json", "source-provenance.json", "SHA256SUMS"):
            self.add_asset(name)
        for extension in scenario.EXTENSIONS:
            artifacts = {}
            version = self.baseline["extensions"][extension]["version"]
            for platform in scenario.PLATFORMS:
                name = extension.replace(".", "-") + "-" + platform.replace("/", "-") + ".zip"
                entry = name.removesuffix(".zip")
                self.add_asset(name)
                artifacts[platform] = {
                    "url": self.base + name, "entryPoint": entry,
                    "checksum": {"algorithm": "sha256", "value": "b" * 64},
                }
                self.provenance["archives"].append({
                    "extension": extension, "platform": platform, "name": name,
                    "version": version, "sha256": "b" * 64, "entryPoint": entry,
                })
            self.registry["extensions"].append({
                "id": extension, "versions": [{"version": version, "artifacts": artifacts}],
            })

    def add_asset(self, name):
        self.assets[name] = {"url": self.base + name, "sha256": "b" * 64}
        self.sums[name] = "b" * 64
        self.release["assets"].append({
            "name": name, "browser_download_url": self.base + name, "digest": "sha256:" + "b" * 64,
        })

    def build(self):
        return scenario.build_manifest(self.release, self.assets, self.registry,
                                       self.provenance, self.sums, self.baseline)

    def test_frozen_manifest_preserves_core_and_contract(self):
        original = copy.deepcopy(self.baseline)
        pin = self.build()
        self.assertEqual(pin["releaseTag"], self.tag)
        self.assertEqual(pin["sourceCommit"], "a" * 40)
        self.assertEqual(pin["sourceVerificationCommit"], pin["sourceCommit"])
        self.assertEqual(pin["azd"], original["azd"])
        self.assertTrue(pin["initDatasetBinding"])
        self.assertEqual(self.baseline, original)
        self.assertEqual(len(pin["scenarioResolution"]["artifacts"]), 2)

    def test_rejects_mismatched_metadata(self):
        for field, value in (("releaseTag", "wrong"), ("sourceCommit", "main"),
                             ("sourceRepository", "https://example.invalid/repo"), ("azdVersion", "0.0.0")):
            with self.subTest(field=field):
                previous = self.provenance[field]
                self.provenance[field] = value
                with self.assertRaises(AssertionError):
                    self.build()
                self.provenance[field] = previous

    def test_rejects_registry_provenance_and_checksum_disagreement(self):
        for field in ("name", "version", "sha256", "entryPoint"):
            with self.subTest(field=field):
                previous = self.provenance["archives"][0][field]
                self.provenance["archives"][0][field] = "wrong"
                with self.assertRaises(AssertionError):
                    self.build()
                self.provenance["archives"][0][field] = previous
        self.sums["registry.json"] = "c" * 64
        with self.assertRaises(AssertionError):
            self.build()

    def test_rejects_non_frozen_or_credential_bearing_archive(self):
        artifact = self.registry["extensions"][0]["versions"][0]["artifacts"]["linux/amd64"]
        for url in ("https://github.com/repo/releases/latest/download/a.zip",
                    "https://user:secret@github.com/a.zip?sig=secret",
                    "https://example.invalid/a.zip"):
            with self.subTest(url=url):
                artifact["url"] = url
                with self.assertRaises(AssertionError):
                    self.build()

    def test_release_api_assets_are_unique_pinned_and_hashed(self):
        self.assertEqual(scenario.asset_map(self.release, self.tag), self.assets)
        self.release["assets"].append(self.release["assets"][0])
        with self.assertRaises(AssertionError):
            scenario.asset_map(self.release, self.tag)
        self.release["assets"].pop()
        self.release["assets"][0]["digest"] = None
        with self.assertRaises(AssertionError):
            scenario.asset_map(self.release, self.tag)

    def test_checksums_reject_duplicates_traversal_and_bad_hashes(self):
        self.assertEqual(scenario.parse_sums(("a" * 64 + "  file.zip\n").encode()), {"file.zip": "a" * 64})
        for text in ("a" * 64 + "  ../file.zip\n", "bad  file.zip\n",
                     ("a" * 64 + "  file.zip\n") * 2):
            with self.assertRaises(AssertionError):
                scenario.parse_sums(text.encode())

    def test_resolve_reads_latest_once_and_refuses_overwrite(self):
        blobs = {"registry.json": json.dumps(self.registry).encode(),
                 "source-provenance.json": json.dumps(self.provenance).encode()}
        for name, data in blobs.items():
            digest = scenario.sha256(data)
            self.sums[name] = digest
            next(item for item in self.release["assets"] if item["name"] == name)["digest"] = "sha256:" + digest
        blobs["SHA256SUMS"] = "".join(
            f"{digest}  {name}\n" for name, digest in self.sums.items() if name != "SHA256SUMS"
        ).encode()
        next(item for item in self.release["assets"] if item["name"] == "SHA256SUMS")["digest"] = (
            "sha256:" + scenario.sha256(blobs["SHA256SUMS"]))
        latest = f"https://api.github.com/repos/{scenario.FEED}/releases/latest"
        urls = {latest: json.dumps(self.release).encode(),
                **{self.base + name: data for name, data in blobs.items()}}
        with tempfile.TemporaryDirectory() as root, mock.patch.object(scenario, "fetch", side_effect=urls.__getitem__) as get:
            path = Path(root) / "frozen.json"
            scenario.resolve(path)
            self.assertEqual(get.call_args_list.count(mock.call(latest)), 1)
            self.assertEqual(get.call_count, 4)
            with self.assertRaises(AssertionError):
                scenario.resolve(path)
            self.assertEqual(get.call_count, 4)


class SafetyTests(unittest.TestCase):
    def setUp(self):
        environment = mock.patch.dict(scenario.os.environ, {})
        environment.start()
        self.addCleanup(environment.stop)
        for key in ("GITHUB_STEP_SUMMARY", "GITHUB_RUN_ID", "BUILD_BUILDID"):
            scenario.os.environ.pop(key, None)

    def test_workflow_tracks_the_shared_candidate_manifest_dependency(self):
        workflow = scenario.HERE.parents[2] / ".github" / "workflows" / "eval-scenario-ci.yml"
        push_paths = workflow.read_text(encoding="utf-8").split("    paths:\n", 1)[1].split(
            "  workflow_dispatch:", 1)[0]
        self.assertIn("      - eng/scripts/eval-candidate-proof/candidate.json\n", push_paths)

    def test_archive_errors_are_recorded_without_suppressing_them(self):
        legacy = scenario.proof_module
        for filename, expected in (("broken.zip", legacy.zipfile.BadZipFile),
                                   ("broken.tar.gz", legacy.tarfile.ReadError)):
            with self.subTest(filename=filename):
                report = {}
                with self.assertRaises(expected):
                    with legacy.owned_workspace(report) as root:
                        archive = root / filename
                        archive.write_bytes(b"checksum-pinned but invalid archive")
                        legacy.binary_from_archive(archive, "fixture")
                self.assertEqual(report["failure"]["type"], expected.__name__)
                self.assertTrue(report["failure"]["message"])
                self.assertEqual(report["cleanup"]["status"], "PASS")

    def test_success_does_not_record_an_ambient_handled_exception(self):
        report = {}
        try:
            raise ValueError("already handled outside the workspace")
        except ValueError:
            with scenario.owned_workspace(report):
                pass
        self.assertNotIn("failure", report)
        self.assertEqual(report["cleanup"]["status"], "PASS")

    def test_command_receipt_records_timing_and_actual_timeout(self):
        legacy = scenario.proof_module
        with tempfile.TemporaryDirectory() as root:
            proof = legacy.Proof(Path(root), Path(root), {})
            completed = legacy.subprocess.CompletedProcess([], 0, '{"ok":true}\n', "")
            with mock.patch.object(legacy.subprocess, "run", return_value=completed) as run, \
                 mock.patch.object(legacy.time, "monotonic", side_effect=[100.0, 100.125]):
                self.assertEqual(proof.run("fixture", ["version"], timeout=23, json_output=True), {"ok": True})
            record = proof.commands[0]
            self.assertEqual(record["timeoutSeconds"], run.call_args.kwargs["timeout"])
            self.assertEqual(record["timeoutSeconds"], 23)
            self.assertEqual(record["durationSeconds"], 0.125)
            started = datetime.fromisoformat(record["startedAt"])
            finished = datetime.fromisoformat(record["finishedAt"])
            self.assertIsNotNone(started.tzinfo)
            self.assertGreaterEqual(finished, started)

    def test_timeout_keeps_bounded_redacted_command_receipt(self):
        legacy = scenario.proof_module
        with tempfile.TemporaryDirectory() as root:
            proof = legacy.Proof(Path(root), Path(root), {})
            expired = legacy.subprocess.TimeoutExpired(
                "azd", 7, output=b"https://user:password@host.invalid/a?sig=secret#fragment", stderr=b"timed out")
            with mock.patch.object(legacy.subprocess, "run", side_effect=expired):
                with self.assertRaises(legacy.subprocess.TimeoutExpired):
                    proof.run("timeout fixture", ["version"], timeout=7)
            record = proof.commands[0]
            self.assertTrue(record["timedOut"])
            self.assertIsNone(record["exitCode"])
            self.assertEqual(record["timeoutSeconds"], 7)
            self.assertGreaterEqual(record["durationSeconds"], 0)
            self.assertIn("finishedAt", record)
            self.assertEqual(record["stdout"], "https://host.invalid/a")
            self.assertEqual(proof.checks, [])

    def test_legacy_install_rejects_duplicate_extension_entries(self):
        legacy = scenario.proof_module
        pin = json.loads((scenario.BASELINE / "candidate.json").read_text())
        with tempfile.TemporaryDirectory() as root:
            root = Path(root)
            proof = legacy.Proof(root, root, pin)
            registry = root / "registry.json"
            legacy.write_json(registry, {"extensions": [
                {"id": "azure.ai.evaluations"}, {"id": "azure.ai.evaluations"}, {"id": "azure.ai.dataset"},
            ]})
            with mock.patch.object(legacy, "download", side_effect=[root / "core.zip", registry]), \
                 mock.patch.object(legacy, "binary_from_archive", return_value=b"not executed"), \
                 mock.patch.object(proof, "run", return_value=f"azd version {pin['azd']['version']}"):
                with self.assertRaisesRegex(AssertionError, "exactly the two"):
                    proof.install()

    def test_canonical_baseline_rejects_missing_duplicate_changed_or_reordered_ids(self):
        legacy = scenario.proof_module
        expected = legacy.expected_baseline_checks()
        legacy.validate_baseline_checks(expected)
        for actual in (expected[:-1], expected[:-1] + [expected[0]],
                       expected[:-1] + ["different check"], list(reversed(expected))):
            with self.assertRaisesRegex(AssertionError, "Baseline check IDs differ"):
                legacy.validate_baseline_checks(actual)

    def test_cancel_arity_diagnostic_does_not_match_unrelated_errors(self):
        self.assertRegex("accepts at most 1 arg(s), received 2", scenario.CANCEL_ARITY_ERROR)
        for message in ("target not found", "invalid argument", "accepts at most 1 arg(s), received 3"):
            self.assertNotRegex(message, scenario.CANCEL_ARITY_ERROR)

    def test_cleanup_retries_only_transient_windows_sharing_failures(self):
        sharing = PermissionError("owned executable still closing")
        sharing.winerror = 32
        with mock.patch.object(scenario.proof_module.shutil, "rmtree", side_effect=[sharing, None]) as remove:
            with mock.patch.object(scenario.proof_module.time, "sleep") as wait:
                scenario.cleanup_owned_workspace(Path("owned-only"))
                self.assertEqual(remove.call_count, 2)
                wait.assert_called_once_with(0.25)
        with mock.patch.object(scenario.proof_module.shutil, "rmtree", side_effect=PermissionError("denied")) as remove:
            with self.assertRaises(PermissionError):
                scenario.cleanup_owned_workspace(Path("owned-only"))
            self.assertEqual(remove.call_count, 1)

    def test_cleanup_failure_cannot_produce_pass_receipt(self):
        pin = json.loads((scenario.BASELINE / "candidate.json").read_text())
        pin["scenarioResolution"] = {"fixtureContract": "build41-offline-160"}
        with tempfile.TemporaryDirectory() as root:
            manifest = Path(root) / "manifest.json"
            scenario.write_json(manifest, pin)
            output = Path(root) / "evidence"
            fake = mock.Mock()
            fake.env = {}
            fake.platform = "windows/amd64"
            fake.commands = []
            fake.checks = scenario.proof_module.expected_baseline_checks()

            def extras(proof):
                proof.checks.extend(["extra"] * 8)

            failure = PermissionError("cleanup failed")
            owned = Path(root) / "owned"
            owned.mkdir()
            with mock.patch.object(scenario.proof_module, "Proof", return_value=fake), \
                 mock.patch.object(scenario, "installed_evidence", return_value={}), \
                 mock.patch.object(scenario, "extra_scenarios", side_effect=extras), \
                 mock.patch.object(scenario.proof_module.tempfile, "mkdtemp", return_value=str(owned)), \
                 mock.patch.object(scenario.proof_module, "cleanup_owned_workspace", side_effect=failure):
                with self.assertRaises(PermissionError):
                    scenario.execute(manifest, output)
            report = json.loads((output / "results.json").read_text())
            self.assertEqual(report["status"], "FAIL")
            self.assertEqual(report["cleanup"]["status"], "FAIL")
            self.assertEqual(report["scenarioCheckCount"], 8)
            self.assertIn("scenarios: FAIL", (output / "summary.md").read_text())

    def test_primary_failure_survives_a_second_cleanup_failure(self):
        pin = json.loads((scenario.BASELINE / "candidate.json").read_text())
        pin["scenarioResolution"] = {"fixtureContract": "build41-offline-160"}
        with tempfile.TemporaryDirectory() as root:
            manifest = Path(root) / "manifest.json"
            scenario.write_json(manifest, pin)
            output = Path(root) / "evidence"
            owned = Path(root) / "owned"
            owned.mkdir()
            fake = mock.Mock()
            fake.env, fake.platform, fake.commands, fake.checks = {}, "windows/amd64", [], ["partial"]
            fake.exercise.side_effect = AssertionError(f"primary failure in {owned}")
            with mock.patch.object(scenario.proof_module, "Proof", return_value=fake), \
                 mock.patch.object(scenario, "installed_evidence", return_value={}), \
                 mock.patch.object(scenario.proof_module.tempfile, "mkdtemp", return_value=str(owned)), \
                 mock.patch.object(scenario.proof_module, "cleanup_owned_workspace",
                                   side_effect=PermissionError("cleanup locked")):
                with self.assertRaises(PermissionError):
                    scenario.execute(manifest, output)
            report = json.loads((output / "results.json").read_text())
            self.assertEqual(report["status"], "FAIL")
            self.assertEqual(report["failure"]["type"], "AssertionError")
            self.assertEqual(report["failure"]["message"], "primary failure in <isolated-work>")
            self.assertEqual(report["cleanup"]["status"], "FAIL")
            self.assertEqual(report["cleanup"]["error"], "cleanup locked")
            summary = (output / "summary.md").read_text()
            self.assertIn("scenarios: FAIL", summary)
            self.assertIn("primary failure in <isolated-work>", summary)
            self.assertIn("cleanup locked", summary)

    def test_success_receipt_keeps_frozen_manifest_bytes_and_waits_for_cleanup(self):
        pin = json.loads((scenario.BASELINE / "candidate.json").read_text())
        pin["scenarioResolution"] = {"fixtureContract": "build41-offline-160"}
        frozen = (json.dumps(pin, indent=2) + "\n").encode("utf-8")
        with tempfile.TemporaryDirectory() as root:
            manifest = Path(root) / "manifest.json"
            manifest.write_bytes(frozen)
            output = Path(root) / "evidence"
            fake = mock.Mock()
            fake.env, fake.platform, fake.commands = {}, "windows/amd64", []
            fake.checks = scenario.proof_module.expected_baseline_checks()
            with mock.patch.object(scenario.proof_module, "Proof", return_value=fake), \
                 mock.patch.object(scenario, "installed_evidence", return_value={}), \
                 mock.patch.object(scenario, "extra_scenarios",
                                   side_effect=lambda proof: proof.checks.extend(["extra"] * 8)):
                scenario.execute(manifest, output)
            self.assertEqual((output / "candidate.json").read_bytes(), frozen)
            report = json.loads((output / "results.json").read_text())
            self.assertEqual(report["manifestSha256"], scenario.sha256(frozen))
            self.assertEqual(report["status"], "PASS")
            self.assertEqual(report["cleanup"]["status"], "PASS")

    def test_ado_run_link_retains_only_known_build_id(self):
        report = scenario.run_identity({
            "BUILD_BUILDID": "42", "BUILD_SOURCEVERSION": "a" * 40,
            "SYSTEM_COLLECTIONURI": "https://user:secret@dev.azure.com/example/?sig=secret",
            "SYSTEM_TEAMPROJECT": "Offline proof",
        })
        self.assertEqual(report["runUrl"],
                         "https://dev.azure.com/example/Offline%20proof/_build/results?buildId=42")
        self.assertNotIn("secret", json.dumps(report))

    def test_legacy_receipt_requires_both_scenario_and_cleanup_success(self):
        legacy = scenario.proof_module
        for scenario_fails, cleanup_fails in ((False, False), (True, False), (False, True), (True, True)):
            with self.subTest(scenario_fails=scenario_fails, cleanup_fails=cleanup_fails):
                with tempfile.TemporaryDirectory() as root:
                    output = Path(root) / "evidence"
                    owned = Path(root) / "owned"
                    owned.mkdir()
                    fake = mock.Mock()
                    fake.platform, fake.commands = "windows/amd64", []
                    fake.checks = legacy.expected_baseline_checks()
                    if scenario_fails:
                        fake.exercise.side_effect = AssertionError("original scenario failure")
                    remove = legacy.cleanup_owned_workspace
                    cleanup = PermissionError("cleanup failure") if cleanup_fails else remove
                    with mock.patch.object(legacy, "Proof", return_value=fake), \
                         mock.patch.object(legacy.tempfile, "mkdtemp", return_value=str(owned)), \
                         mock.patch.object(legacy, "cleanup_owned_workspace", side_effect=cleanup), \
                         mock.patch.object(legacy.argparse.ArgumentParser, "parse_args",
                                           return_value=mock.Mock(output=output)):
                        if scenario_fails or cleanup_fails:
                            with self.assertRaises((AssertionError, PermissionError)):
                                legacy.main()
                        else:
                            legacy.main()
                    report = json.loads((output / "results.json").read_text())
                    self.assertEqual(report["status"], "FAIL" if scenario_fails or cleanup_fails else "PASS")
                    self.assertEqual(report["cleanup"]["status"], "FAIL" if cleanup_fails else "PASS")
                    summary = (output / "summary.md").read_text()
                    if scenario_fails:
                        self.assertEqual(report["failure"]["message"], "original scenario failure")
                        self.assertIn("original scenario failure", summary)
                    if cleanup_fails:
                        self.assertEqual(report["cleanup"]["error"], "cleanup failure")
                        self.assertIn("cleanup failure", summary)

    def test_legacy_dataset_arity_refuses_target_error(self):
        expected = scenario.proof_module.DATASET_ARITY_ERROR
        self.assertRegex("accepts 1 arg(s), received 0", expected)
        self.assertNotRegex("target not found", expected)
        self.assertNotRegex("accepts 1 arg(s), received 2", expected)

    def test_redacts_url_credentials_query_and_fragment(self):
        text = scenario.safe_text("failed https://username:password@host.invalid/a?sig=sas-secret#fragment-secret")
        for secret in ("username", "password", "sas-secret", "fragment-secret"):
            self.assertNotIn(secret, text)
        self.assertIn("https://host.invalid/a", text)

    def test_untrusted_fetch_never_reaches_network(self):
        with mock.patch("urllib.request.urlopen") as open_url:
            for url in ("http://github.com/a", "https://user:secret@github.com/a",
                        "https://github.com/a?sig=secret", "https://elsewhere.invalid/a"):
                with self.assertRaises(AssertionError):
                    scenario.fetch(url)
            open_url.assert_not_called()

    def test_child_has_no_parent_credentials_or_config(self):
        with tempfile.TemporaryDirectory() as root, mock.patch.dict(scenario.os.environ, {
            "AZURE_CLIENT_SECRET": "do-not-copy", "GITHUB_TOKEN": "do-not-copy",
            "SYSTEM_ACCESSTOKEN": "do-not-copy", "AZD_CONFIG_DIR": "do-not-use",
        }):
            proof = scenario.proof_module.Proof(Path(root), Path(root), {})
            for key in ("AZURE_CLIENT_SECRET", "GITHUB_TOKEN", "SYSTEM_ACCESSTOKEN"):
                self.assertNotIn(key, proof.env)
            self.assertEqual(proof.env["AZD_CONFIG_DIR"], str(Path(root) / "config"))

    def test_live_request_fails_closed_with_durable_not_run_evidence(self):
        with tempfile.TemporaryDirectory() as root:
            output = Path(root) / "live"
            with mock.patch.object(scenario.sys, "argv", ["scenario.py", "live", "--output", str(output)]):
                self.assertEqual(scenario.main(), 3)
            report = json.loads((output / "live-status.json").read_text())
            self.assertEqual(report["status"], "BLOCKED")
            self.assertEqual(report["execution"], "NOT RUN")
            self.assertTrue(report["blockers"])


if __name__ == "__main__":
    unittest.main()
