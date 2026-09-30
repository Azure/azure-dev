# Copyright (c) Microsoft Corporation. All rights reserved.
# Licensed under the MIT License.

import copy
import http.client
import json
from pathlib import Path
import tempfile
import unittest
from unittest import mock

import scenario
import test_scenario


class FrozenMetadataTests(unittest.TestCase):
    def setUp(self):
        fixture = test_scenario.ResolutionTests()
        fixture.setUp()
        latest, self.urls = fixture.resolution_urls()
        self.tag_url = f"https://api.github.com/repos/{scenario.FEED}/releases/tags/{fixture.tag}"
        self.urls[self.tag_url] = self.urls.pop(latest)
        self.approved, self.authority = fixture.baseline, fixture.authority
        self.pin = scenario.build_manifest(
            fixture.release, scenario.asset_map(fixture.release, fixture.tag), fixture.registry,
            fixture.provenance, scenario.parse_sums(self.urls[fixture.base + "SHA256SUMS"]),
            self.approved, self.authority)

    def test_consumer_refetches_fixed_tag_metadata_without_querying_latest(self):
        with mock.patch.object(scenario, "fetch", side_effect=self.urls.__getitem__) as fetch:
            scenario.verify_frozen_metadata(self.pin, self.approved, self.authority)
        self.assertEqual(fetch.call_count, 4)
        self.assertEqual(fetch.call_args_list[0], mock.call(self.tag_url))
        self.assertFalse(any("/latest" in call.args[0] for call in fetch.call_args_list))

    def test_same_shape_false_metadata_claims_block_before_installer(self):
        for name in ("source-provenance.json", "SHA256SUMS"):
            with self.subTest(name=name), tempfile.TemporaryDirectory() as root:
                pin = copy.deepcopy(self.pin)
                pin["scenarioResolution"]["metadata"][name]["sha256"] = "0" * 64
                root = Path(root)
                path = root / "candidate.json"
                scenario.write_json(path, pin)
                with mock.patch.object(scenario, "reviewed_candidate", return_value=(self.approved, self.authority)), \
                     mock.patch.object(scenario, "fetch", side_effect=self.urls.__getitem__), \
                     mock.patch.object(scenario.proof_module, "Proof") as proof:
                    with self.assertRaises(scenario.ApprovalBlocked):
                        scenario.execute(path, root / "evidence")
                    proof.assert_not_called()
                receipt = json.loads((root / "evidence" / "approval-status.json").read_bytes())
                self.assertEqual((receipt["status"], receipt["execution"]), ("BLOCKED", "NOT RUN"))
                self.assertFalse((root / "evidence" / "candidate.json").exists())

    def test_fetched_bytes_must_match_metadata_digests(self):
        for name, record in self.pin["scenarioResolution"]["metadata"].items():
            with self.subTest(name=name), mock.patch.object(
                    scenario, "fetch", side_effect={**self.urls, record["url"]: b"substituted"}.__getitem__):
                with self.assertRaises(scenario.ApprovalBlocked):
                    scenario.verify_frozen_metadata(self.pin, self.approved, self.authority)

    def test_fixed_release_identity_and_source_claims_must_match(self):
        for field, value in (("releaseId", 999), ("publishedAt", "2026-01-01T00:00:00Z")):
            with self.subTest(field=field), mock.patch.object(scenario, "fetch", side_effect=self.urls.__getitem__):
                pin = copy.deepcopy(self.pin)
                pin["scenarioResolution"][field] = value
                with self.assertRaises(scenario.ApprovalBlocked):
                    scenario.verify_frozen_metadata(pin, self.approved, self.authority)

    def test_incomplete_body_preserves_producer_and_consumer_block_receipts(self):
        for operation in ("resolve", "offline", "candidate-approval"):
            with self.subTest(operation=operation), tempfile.TemporaryDirectory() as root:
                root = Path(root)
                path = root / "candidate.json"
                scenario.write_json(path, self.approved if operation == "candidate-approval" else self.pin)
                response = mock.MagicMock()
                response.__enter__.return_value.read.side_effect = http.client.IncompleteRead(b"private-partial-body", 10)
                env = {"AZD_SCENARIO_APPROVAL_REPOSITORY": "trusted/repository",
                       "AZD_SCENARIO_APPROVED_COMMIT": "e" * 40}
                with mock.patch.dict(scenario.os.environ, env), \
                     mock.patch.object(scenario.urllib.request, "urlopen", return_value=response), \
                     mock.patch.object(scenario.proof_module, "Proof") as proof:
                    with self.assertRaises(scenario.ApprovalBlocked):
                        if operation == "resolve":
                            scenario.resolve(root / "evidence" / "candidate.json")
                        elif operation == "offline":
                            scenario.execute(path, root / "evidence")
                        else:
                            scenario.approve_candidate(path, root / "evidence")
                    proof.assert_not_called()
                receipt = json.loads((root / "evidence" / "approval-status.json").read_bytes())
                self.assertEqual((receipt["status"], receipt["execution"]), ("BLOCKED", "NOT RUN"))
                self.assertNotIn("private-partial-body", json.dumps(receipt))


if __name__ == "__main__":
    unittest.main()
