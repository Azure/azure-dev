# Copyright (c) Microsoft Corporation. All rights reserved.
# Licensed under the MIT License.

import copy
import json
from pathlib import Path
import tempfile
import unittest
from unittest import mock

import scenario
from test_scenario import producer_manifest


class InstalledRouteTests(unittest.TestCase):
    def setUp(self):
        directory = tempfile.TemporaryDirectory()
        self.addCleanup(directory.cleanup)
        self.root = Path(directory.name)
        self.legacy = scenario.proof_module
        pin = json.loads((scenario.BASELINE / "candidate.json").read_bytes())
        self.pin = producer_manifest(pin, {})
        self.proof = self.legacy.Proof(self.root, self.root, self.pin)
        self.config = self.root / "config"
        self.settings = {"extension": {"installed": {}}}
        self.registry = {"extensions": []}
        self.proof.azd.write_bytes(b"approved")
        core = self.pin["azd"]["artifacts"][self.proof.platform]
        self.core_archive = self.root / "downloads" / core["file"]
        self.core_archive.write_bytes(b"archive")
        core["sha256"] = scenario.sha256(b"archive")
        for extension, pin in self.pin["extensions"].items():
            asset = self.pin["scenarioResolution"]["artifacts"][extension][self.proof.platform]
            archive = self.root / "downloads" / asset["url"].rsplit("/", 1)[-1]
            archive.write_bytes(b"archive")
            asset["sha256"] = pin["artifacts"][self.proof.platform] = scenario.sha256(b"archive")
            relative = Path("extensions") / extension / "fixture.exe"
            conventional = self.config / relative
            conventional.parent.mkdir(parents=True)
            conventional.write_bytes(b"approved")
            self.settings["extension"]["installed"][extension] = {
                "id": extension, "namespace": "ai." + pin["command"],
                "version": pin["version"], "path": str(relative),
            }
            self.registry["extensions"].append({
                "id": extension, "versions": [{
                    "version": pin["version"], "artifacts": {self.proof.platform: {
                        "entryPoint": "fixture.exe", "url": asset["url"],
                        "checksum": {"algorithm": "sha256", "value": asset["sha256"]},
                    }},
                }],
            })
        self.extension = next(iter(self.pin["extensions"]))
        self.record = self.settings["extension"]["installed"][self.extension]
        self.registry_path = self.root / "registry.json"
        scenario.write_json(self.registry_path, self.registry)
        self.save_settings()

    def save_settings(self):
        scenario.write_json(self.config / "config.json", self.settings)

    def redirect(self, data):
        path = self.config / "redirected.exe"
        path.write_bytes(data)
        self.record["path"] = path.name
        self.save_settings()
        return path

    def run_install(self):
        def download(url, digest, directory):
            if url.endswith("registry.json"):
                return self.registry_path
            return directory / url.rsplit("/", 1)[-1]

        def run(name, args, **kwargs):
            if name == "azd version":
                return f"azd version {self.pin['azd']['version']}"
            for extension, pin in self.pin["extensions"].items():
                if name == f"{extension} version JSON":
                    return {"name": extension, "version": pin["version"]}
            return ""

        with mock.patch.object(self.legacy, "download", side_effect=download), \
             mock.patch.object(self.legacy, "binary_from_archive", return_value=b"approved"), \
             mock.patch.object(self.proof, "run", side_effect=run) as commands:
            self.commands = commands
            self.proof.install()

    def test_redirected_unapproved_bytes_fail_before_extension_version_runs(self):
        self.redirect(b"unapproved")
        with self.assertRaisesRegex(AssertionError, "Installed binary differs"):
            self.run_install()
        self.assertFalse(any(call.args[0].endswith("version JSON") for call in self.commands.call_args_list))
        self.assertEqual((self.config / "extensions" / self.extension / "fixture.exe").read_bytes(), b"approved")

    def test_approved_persisted_route_is_used_instead_of_reconstructed_archive_path(self):
        path = self.redirect(b"approved")
        (self.config / "extensions" / self.extension / "fixture.exe").write_bytes(b"wrong unused bytes")
        self.assertEqual(self.proof.installed_extension_path(self.extension), path.resolve())
        self.run_install()
        with mock.patch.object(self.legacy, "binary_from_archive", return_value=b"approved"):
            records = scenario.installed_evidence(self.proof, self.pin)
        self.assertEqual(records[self.extension]["installedBinarySha256"], scenario.sha256(b"approved"))

    def test_receipt_rechecks_redirected_route_even_when_conventional_file_matches(self):
        self.run_install()
        self.redirect(b"unapproved")
        with mock.patch.object(self.legacy, "binary_from_archive", return_value=b"approved"):
            with self.assertRaisesRegex(AssertionError, "Installed/archive byte comparison failed"):
                scenario.installed_evidence(self.proof, self.pin)

    def test_installed_identity_namespace_and_version_must_match_pin(self):
        original = copy.deepcopy(self.record)
        for field, value in (("id", "different"), ("namespace", "ai.other"), ("version", "0.0.0")):
            with self.subTest(field=field):
                self.record.update(original)
                self.record[field] = value
                self.save_settings()
                with self.assertRaisesRegex(AssertionError, "routing or version metadata"):
                    self.run_install()
                self.assertFalse(any(call.args[0].endswith("version JSON") for call in self.commands.call_args_list))

    def test_uncontained_missing_or_implicit_executable_paths_are_rejected(self):
        paths = [None, "", str(self.root / "outside.exe"), "../outside.exe", r"..\outside.exe",
                 r"C:\outside.exe", r"C:outside.exe", r"\outside.exe", r"\\host\share\outside.exe",
                 "fixture.exe:stream", "missing.exe"]
        if self.legacy.os.name == "nt":
            paths.append("implicit-extension")
        for path in paths:
            with self.subTest(path=path):
                self.record["path"] = path
                self.save_settings()
                with self.assertRaises(AssertionError):
                    self.proof.installed_extension_path(self.extension)

    def test_symlink_route_cannot_escape_profile(self):
        outside = self.root / "outside.exe"
        outside.write_bytes(b"approved")
        link = self.config / "linked.exe"
        try:
            link.symlink_to(outside)
        except OSError as error:
            self.skipTest(f"Symlink creation unavailable: {error}")
        self.record["path"] = link.name
        self.save_settings()
        with self.assertRaisesRegex(AssertionError, "escapes the isolated profile"):
            self.proof.installed_extension_path(self.extension)


if __name__ == "__main__":
    unittest.main()
