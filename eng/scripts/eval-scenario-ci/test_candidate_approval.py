# Copyright (c) Microsoft Corporation. All rights reserved.
# Licensed under the MIT License.

import copy
import json
from pathlib import Path
import tempfile
import unittest
from unittest import mock

import scenario


class CandidateApprovalTests(unittest.TestCase):
    def setUp(self):
        self.pin = json.loads((scenario.BASELINE / "candidate.json").read_bytes())
        self.authority = {"repository": "trusted/repository", "commit": "e" * 40,
                          "path": "eng/scripts/eval-candidate-proof/candidate.json", "sha256": "f" * 64}

    def test_exact_candidate_approval_returns_only_independently_authorized_source(self):
        with tempfile.TemporaryDirectory() as root:
            root = Path(root)
            path = root / "candidate.json"
            path.write_bytes((json.dumps(self.pin, indent=2) + "\r\n").encode())
            with mock.patch.object(scenario, "reviewed_candidate", return_value=(self.pin, self.authority)), \
                 mock.patch.object(scenario.proof_module, "Proof") as proof:
                result = scenario.approve_candidate(path, root / "evidence")
                self.assertEqual(result, self.pin)
                proof.assert_not_called()
            receipt = json.loads((root / "evidence" / "approval-status.json").read_bytes())
            self.assertEqual(receipt["status"], "PASS")
            self.assertEqual(receipt["execution"], "NOT RUN")
            self.assertEqual(receipt["approval"], self.authority)
            self.assertEqual(receipt["sourceCommit"], self.pin["sourceCommit"])

    def test_changed_source_core_registry_or_flags_never_authorizes_execution(self):
        changes = []
        for field in ("sourceCommit", "sourceVerificationCommit"):
            pin = copy.deepcopy(self.pin)
            pin[field] = "a" * 40
            changes.append(pin)
        pin = copy.deepcopy(self.pin)
        pin["azd"]["artifacts"]["linux/amd64"]["sha256"] = "a" * 64
        changes.append(pin)
        changes.extend([{**self.pin, "registrySha256": "a" * 64}, {**self.pin, "initSeedValidation": 1}])
        for pin in changes:
            with self.subTest(pin=pin), tempfile.TemporaryDirectory() as root:
                root = Path(root)
                path = root / "candidate.json"
                scenario.write_json(path, pin)
                with mock.patch.object(scenario, "reviewed_candidate", return_value=(self.pin, self.authority)), \
                     mock.patch.object(scenario.proof_module, "Proof") as proof:
                    with self.assertRaises(scenario.ApprovalBlocked):
                        scenario.approve_candidate(path, root / "evidence")
                    proof.assert_not_called()
                receipt = json.loads((root / "evidence" / "approval-status.json").read_bytes())
                self.assertEqual((receipt["status"], receipt["execution"]), ("BLOCKED", "NOT RUN"))
                self.assertNotIn("sourceCommit", receipt)

    def test_missing_or_malformed_checkout_pins_block_before_approval_lookup(self):
        for raw in (None, b"{broken", b"\xff", b"null", b'{"azd":{},"azd":{}}'):
            with self.subTest(raw=raw), tempfile.TemporaryDirectory() as root:
                root = Path(root)
                path = root / "candidate.json"
                if raw is not None:
                    path.write_bytes(raw)
                with mock.patch.object(scenario, "reviewed_candidate") as approval:
                    with self.assertRaises(scenario.ApprovalBlocked):
                        scenario.approve_candidate(path, root / "evidence")
                    approval.assert_not_called()
                self.assertEqual(json.loads((root / "evidence" / "approval-status.json").read_bytes())["status"], "BLOCKED")

    def test_missing_independent_approval_never_returns_source_for_checkout(self):
        with tempfile.TemporaryDirectory() as root:
            root = Path(root)
            path = root / "candidate.json"
            scenario.write_json(path, self.pin)
            with mock.patch.object(scenario, "reviewed_candidate",
                                   side_effect=scenario.ApprovalBlocked("Native approval missing")):
                with self.assertRaises(scenario.ApprovalBlocked):
                    scenario.approve_candidate(path, root / "evidence")
            receipt = json.loads((root / "evidence" / "approval-status.json").read_bytes())
            self.assertEqual(receipt["execution"], "NOT RUN")
            self.assertNotIn("sourceCommit", receipt)

    def test_workflow_gates_both_binary_and_source_execution_independently(self):
        workflow = (scenario.HERE.parents[2] / ".github" / "workflows" / "eval-candidate-proof.yml").read_text()
        offline, source = workflow.split("  offline-cli:\n")[1].split("  source-race:\n")
        self.assertLess(offline.index("candidate-approval"), offline.index("Install pinned artifacts"))
        self.assertLess(source.index("scenario.approve_candidate"), source.index("Checkout exact approved source"))
        self.assertIn("AZD_SCENARIO_APPROVAL_REPOSITORY: ${{ github.repository }}", workflow)
        self.assertIn("AZD_SCENARIO_APPROVED_COMMIT: ${{ vars.AZD_SCENARIO_APPROVED_COMMIT }}", workflow)
        self.assertIn("candidate-approval-cli-", offline)
        self.assertIn("candidate-approval-source-", source)
        self.assertIn("go test -race -count=1 -timeout 15m ./internal/...", source)


if __name__ == "__main__":
    unittest.main()
