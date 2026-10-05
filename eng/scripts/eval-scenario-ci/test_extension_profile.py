# Copyright (c) Microsoft Corporation. All rights reserved.
# Licensed under the MIT License.

import copy
import json
from pathlib import Path
import tempfile
import unittest
from unittest import mock

import service
import test_agent_cli
import test_owned_prompt
import test_service


class ExtensionProfileTests(unittest.TestCase):
    def fixture(self, root):
        return test_agent_cli.AgentCliTests().installed_fixture(root)

    def test_dependency_complete_profile_is_verified_from_independent_registry(self):
        with tempfile.TemporaryDirectory() as root:
            root = Path(root)
            plan, settings, _ = self.fixture(root)
            self.assertEqual(len(settings["extension"]["installed"]), 7)
            receipt = {}
            with mock.patch.object(service, "run_owned_process") as run, \
                 mock.patch.object(service, "Driver") as driver, \
                 mock.patch.object(service.scenario, "fetch") as fetch:
                self.assertEqual(service.verify_install(plan, root, receipt), (root / "azd").resolve())
                run.assert_not_called()
                driver.assert_not_called()
                fetch.assert_not_called()
            self.assertEqual(set(receipt["extensionProfile"]["extensions"]), set(plan["versions"]))
            self.assertEqual(receipt["extensionProfile"]["registrySha256"],
                             plan["extensionProfile"]["registrySha256"])
            for extension, record in receipt["extensionProfile"]["extensions"].items():
                self.assertEqual(record["verifiedBinarySha256"], plan["binarySha256"][extension])
                self.assertRegex(record["approvedArchiveSha256"], r"^[0-9a-f]{64}$")
                self.assertIn("approvedEntryPoint", record)
                self.assertNotIn("entryPoint", record)
                installed = settings["extension"]["installed"][extension]
                self.assertIn("custom-commands", installed["capabilities"])
                self.assertEqual(installed["providers"], test_agent_cli.PROFILE_PROVIDERS.get(extension, []))

    def test_invalid_profiles_block_before_any_executable_or_driver(self):
        cases = ("missing-installed", "extra-installed", "bad-binary", "missing-binary-hash",
                 "bad-version", "installed-namespace", "escaping-path", "absolute-path",
                 "registry-hash", "duplicate-registry-key", "missing-registry-dependency",
                 "undeclared-extra", "cycle", "bad-constraint", "unsupported-constraint", "core-too-old",
                 "missing-archive-hash", "unsafe-artifact", "bad-entrypoint",
                 "registry-route-collision", "missing-platform", "unapproved-version",
                 "installed-dependencies", "installed-providers", "provider-collision",
                 "path-collision", "duplicate-edge", "boolean-version", "malformed-profile")
        dependency_id = "azure.ai.connections"
        reasons = {
            "missing-installed": "exactly the mode-specific",
            "extra-installed": "exactly the mode-specific",
            "bad-binary": "approved bytes",
            "missing-binary-hash": "binary digests",
            "bad-version": "routing or version",
            "installed-namespace": "routing or version",
            "escaping-path": "contained executable path",
            "absolute-path": "relative to the isolated profile",
            "registry-hash": "registry differs from approved bytes",
            "duplicate-registry-key": "duplicate JSON",
            "missing-registry-dependency": "complete supported closure",
            "undeclared-extra": "missing or unreachable",
            "cycle": "contains a cycle",
            "bad-constraint": "dependency version does not satisfy",
            "unsupported-constraint": "complete major.minor.patch SemVer",
            "core-too-old": "core version does not satisfy",
            "missing-archive-hash": "Every approved artifact requires",
            "unsafe-artifact": "credential-free fixed-tag",
            "bad-entrypoint": "contained executable",
            "registry-route-collision": "namespace is unsupported or collides",
            "missing-platform": "lacks an artifact",
            "unapproved-version": "Registry version differs",
            "installed-dependencies": "dependency declarations differ",
            "installed-providers": "capability/provider routing differs",
            "provider-collision": "provider routes collide",
            "path-collision": "executable paths collide",
            "duplicate-edge": "duplicate or malformed approved dependency",
            "boolean-version": "mode-specific approved extension versions",
            "malformed-profile": "bounded SemVer strings",
        }
        for case in cases:
            with self.subTest(case=case), tempfile.TemporaryDirectory() as root:
                root = Path(root)
                plan, settings, _ = self.fixture(root)
                installed = settings["extension"]["installed"]
                registry_path = Path(plan["extensionProfile"]["registryFile"])
                registry = json.loads(registry_path.read_bytes())
                entries = {entry["id"]: entry for entry in registry["extensions"]}
                agent = entries[service.AGENT_EXTENSION]["versions"][0]
                dependency = entries[dependency_id]["versions"][0]
                artifact = next(iter(dependency["artifacts"].values()))
                if case == "missing-installed":
                    del installed[dependency_id]
                elif case == "extra-installed":
                    installed["unapproved"] = copy.deepcopy(installed[dependency_id])
                elif case == "bad-binary":
                    (root / installed[dependency_id]["path"]).write_bytes(b"tampered")
                elif case == "missing-binary-hash":
                    del plan["binarySha256"][dependency_id]
                elif case == "bad-version":
                    installed[dependency_id]["version"] = "1.0.0-beta.99"
                elif case == "installed-namespace":
                    installed[dependency_id]["namespace"] = "ai.eval"
                elif case == "escaping-path":
                    installed[dependency_id]["path"] = "../outside.exe"
                elif case == "absolute-path":
                    installed[dependency_id]["path"] = str((root / "outside.exe").resolve())
                elif case == "registry-hash":
                    plan["extensionProfile"]["registrySha256"] = "0" * 64
                elif case == "missing-registry-dependency":
                    registry["extensions"].remove(entries[dependency_id])
                elif case == "undeclared-extra":
                    agent["dependencies"] = [item for item in agent["dependencies"] if item["id"] != "azure.ai.inspector"]
                elif case == "cycle":
                    dependency["dependencies"] = [{"id": service.AGENT_EXTENSION, "version": "~1.0.0-beta.16"}]
                elif case == "bad-constraint":
                    agent["dependencies"][0]["version"] = "~2.0.0-beta.1"
                elif case == "unsupported-constraint":
                    agent["dependencies"][0]["version"] = "*"
                elif case == "core-too-old":
                    plan["extensionProfile"]["coreVersion"] = "1.33.0"
                elif case == "missing-archive-hash":
                    del artifact["checksum"]
                elif case == "unsafe-artifact":
                    artifact["url"] += "?sig=private-token"
                elif case == "bad-entrypoint":
                    artifact["entryPoint"] = "../outside"
                elif case == "registry-route-collision":
                    entries[dependency_id]["namespace"] = "ai.eval"
                elif case == "missing-platform":
                    dependency["artifacts"] = {}
                elif case == "unapproved-version":
                    dependency["version"] = "1.0.0-beta.99"
                elif case == "installed-dependencies":
                    installed[service.AGENT_EXTENSION]["dependencies"] = []
                elif case == "installed-providers":
                    installed[dependency_id]["providers"] = [{"name": "azure.ai.eval", "type": "service-target"}]
                elif case == "provider-collision":
                    route = {"name": "shared-provider", "type": "service-target"}
                    agent["providers"] = [route]
                    dependency["providers"] = [route]
                elif case == "path-collision":
                    installed[dependency_id]["path"] = installed[service.AGENT_EXTENSION]["path"]
                    plan["binarySha256"][dependency_id] = plan["binarySha256"][service.AGENT_EXTENSION]
                elif case == "duplicate-edge":
                    agent["dependencies"].append(copy.deepcopy(agent["dependencies"][0]))
                elif case == "boolean-version":
                    plan["versions"][dependency_id] = True
                elif case == "malformed-profile":
                    plan["extensionProfile"]["coreVersion"] = True
                registry_raw = json.dumps(registry).encode()
                if case == "duplicate-registry-key":
                    registry_raw = registry_raw.replace(b'"schemaVersion": "1.0"', b'"schemaVersion":"9","schemaVersion":"1.0"')
                registry_path.write_bytes(registry_raw)
                if case != "registry-hash":
                    # The test authorizes these bytes to prove shape/constraint checks,
                    # not just that the external plan digest catches tampering.
                    plan["extensionProfile"]["registrySha256"] = service.scenario.sha256(registry_raw)
                (root / "config.json").write_text(json.dumps(settings))
                row = root / "row.jsonl"
                row.write_bytes(test_owned_prompt.ROW)
                plan["datasetFile"] = str(row)
                raw = json.dumps(plan).encode()
                plan_path = root / "plan.json"
                plan_path.write_bytes(raw)
                env = {**test_service.ServiceTests().github_env(),
                       "AZD_SCENARIO_LIVE_APPROVAL_SHA256": service.scenario.sha256(raw),
                       "AZD_SCENARIO_LIVE_AUTH_CONFIG": str(root)}
                with mock.patch.object(service, "run_owned_process") as run, \
                     mock.patch.object(service, "Driver") as driver:
                    with self.assertRaisesRegex(service.Blocked, reasons[case]):
                        service.execute(plan_path, root / "evidence", env)
                    run.assert_not_called()
                    driver.assert_not_called()
                receipt = json.loads((root / "evidence" / "service-status.json").read_bytes())
                self.assertEqual((receipt["status"], receipt["execution"]), ("BLOCKED", "NOT RUN"))
                self.assertNotIn("private-token", json.dumps(receipt))

    def test_complete_registry_does_not_self_authorize_the_plan(self):
        with tempfile.TemporaryDirectory() as root:
            root = Path(root)
            plan, _, _ = self.fixture(root)
            row = root / "row.jsonl"
            row.write_bytes(test_owned_prompt.ROW)
            plan["datasetFile"] = str(row)
            path = root / "plan.json"
            path.write_bytes(json.dumps(plan).encode())
            env = {**test_service.ServiceTests().github_env(),
                   "AZD_SCENARIO_LIVE_AUTH_CONFIG": str(root)}
            with mock.patch.object(service, "approved_extension_profile") as profile, \
                 mock.patch.object(service, "run_owned_process") as run, \
                 mock.patch.object(service, "Driver") as driver:
                with self.assertRaisesRegex(service.Blocked, "approval digest"):
                    service.execute(path, root / "evidence", env)
                profile.assert_not_called()
                run.assert_not_called()
                driver.assert_not_called()

    def test_missing_command_capability_blocks_even_when_both_metadata_copies_match(self):
        for extension in service.required_extensions(test_agent_cli.plan_for()):
            for omit in (False, True):
                with self.subTest(extension=extension, omit=omit), tempfile.TemporaryDirectory() as root:
                    root = Path(root)
                    plan, settings, _ = self.fixture(root)
                    path = Path(plan["extensionProfile"]["registryFile"])
                    registry = json.loads(path.read_bytes())
                    version = next(entry for entry in registry["extensions"] if entry["id"] == extension)["versions"][0]
                    installed = settings["extension"]["installed"][extension]
                    if omit:
                        del version["capabilities"], installed["capabilities"]
                    else:
                        version["capabilities"] = installed["capabilities"] = []
                    raw = json.dumps(registry).encode()
                    path.write_bytes(raw)
                    plan["extensionProfile"]["registrySha256"] = service.scenario.sha256(raw)
                    (root / "config.json").write_text(json.dumps(settings))
                    row = root / "row.jsonl"
                    row.write_bytes(test_owned_prompt.ROW)
                    plan["datasetFile"] = str(row)
                    plan_path = root / "plan.json"
                    raw_plan = json.dumps(plan).encode()
                    plan_path.write_bytes(raw_plan)
                    env = {**test_service.ServiceTests().github_env(),
                           "AZD_SCENARIO_LIVE_APPROVAL_SHA256": service.scenario.sha256(raw_plan),
                           "AZD_SCENARIO_LIVE_AUTH_CONFIG": str(root)}
                    with mock.patch.object(service, "run_owned_process") as run, \
                         mock.patch.object(service, "Driver",
                                           side_effect=AssertionError("Unbound command must not reach Driver")) as driver:
                        with self.assertRaisesRegex(service.Blocked, "custom-commands"):
                            service.execute(plan_path, root / "evidence", env)
                        run.assert_not_called()
                        driver.assert_not_called()
                    receipt = json.loads((root / "evidence" / "service-status.json").read_bytes())
                    self.assertEqual((receipt["status"], receipt["execution"]), ("BLOCKED", "NOT RUN"))

    def test_supported_constraint_subset_matches_core_semver_rules(self):
        for constraint, version, expected in (
            ("1.0.0-beta.6", "1.0.0-beta.6", True),
            ("= 1.0.0-beta.6", "1.0.0-beta.7", False),
            ("~1.0.0-beta.6", "1.0.0-beta.7", True),
            ("~1.0.0-beta.6", "1.0.0-beta.10", True),
            ("~1.0.0-beta.6", "1.0.0-beta.5", False),
            ("~1.0.0-beta.6", "1.1.0", False),
            ("~1.0.0-beta.6", "1.0.0", True),
            ("~1.0.0-beta.6", "1.0.1-beta.1", True),
            (">=1.34.2", "1.34.2", True),
            (">= 1.34.2", "1.35.0", True),
            (">=1.34.2", "1.34.1", False),
            (">=1.34.2", "1.35.0-beta.1", False),
            (">=1.34.2-0", "1.35.0-beta.1", True),
            ("1.34.2+build.1", "1.34.2+build.2", True),
        ):
            with self.subTest(constraint=constraint, version=version):
                self.assertEqual(service.profile_constraint_matches(constraint, version), expected)
        for constraint in ("", "latest", "*", "^1.0.0", ">=1.0.0,<2.0.0", "1.2", "01.0.0",
                           "1.0.0-beta.01", None, 7):
            with self.subTest(constraint=constraint), self.assertRaises(service.Blocked):
                service.profile_constraint_matches(constraint, "1.0.0")

    def test_runtime_versions_use_only_verified_output_contracts(self):
        with tempfile.TemporaryDirectory() as root:
            root = Path(root)
            plan, _, _ = self.fixture(root)
            driver = test_agent_cli.FakeCliDriver(self, root)
            service.verify_identity(plan, driver)
            self.assertEqual(driver.calls[0], "verify approved core")
            for dependency in test_agent_cli.DEPENDENCY_COMMANDS:
                self.assertIn("verify approved " + dependency, driver.calls)
            for invalid_id in ("azd", *test_agent_cli.DEPENDENCY_COMMANDS):
                labels = []

                def wrong_version(label, args, **kwargs):
                    labels.append(label)
                    if invalid_id == "azd" and label == "verify approved core":
                        return b"azd version 1.33.0 (commit wrong)\n"
                    if label == "verify approved " + invalid_id:
                        self.assertNotIn("output_format", kwargs)
                        return {"name": invalid_id, "version": "1.0.0-beta.999"}
                    return driver(label, args, **kwargs)

                with self.subTest(invalid_id=invalid_id), self.assertRaises(service.Blocked):
                    service.verify_identity(plan, wrong_version)
                if invalid_id == "azd":
                    self.assertEqual(labels, ["verify approved core"])

    def test_legacy_profile_without_registry_approval_cannot_activate_agent_mode(self):
        plan = test_agent_cli.plan_for()
        del plan["extensionProfile"]
        with self.assertRaisesRegex(service.Blocked, "required fields"):
            service.validate_plan(plan, "b" * 64, test_service.ServiceTests().github_env())

    def test_legacy_install_without_dependency_snapshot_still_checks_approved_closure(self):
        with tempfile.TemporaryDirectory() as root:
            root = Path(root)
            plan, settings, _ = self.fixture(root)
            for record in settings["extension"]["installed"].values():
                record.pop("dependencies")
            (root / "config.json").write_text(json.dumps(settings))
            self.assertEqual(service.verify_install(plan, root), (root / "azd").resolve())

    def test_contained_custom_dependency_path_still_requires_the_approved_bytes(self):
        with tempfile.TemporaryDirectory() as root:
            root = Path(root)
            plan, settings, _ = self.fixture(root)
            record = settings["extension"]["installed"]["azure.ai.projects"]
            original = root / record["path"]
            custom = root / "custom" / "approved-project.exe"
            custom.parent.mkdir()
            custom.write_bytes(original.read_bytes())
            original.write_bytes(b"no longer selected")
            record["path"] = str(custom.relative_to(root))
            (root / "config.json").write_text(json.dumps(settings))
            self.assertEqual(service.verify_install(plan, root), (root / "azd").resolve())
            custom.write_bytes(b"not the approved dependency")
            with self.assertRaisesRegex(service.Blocked, "approved bytes"):
                service.verify_install(plan, root)


if __name__ == "__main__":
    unittest.main()
