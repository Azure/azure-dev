import json
import sys
import tempfile
import unittest
from pathlib import Path

sys.path.insert(0, str(Path(__file__).parent))

from sanitize_results import EXPECTED_FIXTURE, EvidenceError, build_evidence, write_evidence

REPOSITORY_ROOT = Path(__file__).resolve().parents[3]


def completed_summary(*, passed: int, failed: int, errored: int = 0) -> dict:
    return {
        "id": "evalrun-sensitive",
        "status": "completed",
        "report_url": "https://user:password@example.test/report?sig=secret",
        "result_counts": {
            "total": passed + failed + errored,
            "passed": passed,
            "failed": failed,
            "errored": errored,
            "skipped": 0,
        },
    }


def exported_items(scores: list[float]) -> dict:
    return {
        "run": {
            "id": "evalrun-sensitive",
            "portal_url": "https://example.test/?sig=secret",
        },
        "items": [
            {
                "id": f"item-{index}",
                "run_id": "evalrun-sensitive",
                "datasource_item": {
                    **row,
                    "service_identifier": "do-not-copy",
                },
                "results": [
                    {
                        "name": "f1_score",
                        "score": score,
                        "passed": score >= 0.9,
                        "reason": "raw service explanation",
                        "sample": {"token": "secret"},
                    }
                ],
            }
            for index, (row, score) in enumerate(zip(EXPECTED_FIXTURE, scores, strict=True))
        ],
    }


class BuildEvidenceTests(unittest.TestCase):
    def test_writes_only_allowlisted_passed_evidence(self) -> None:
        evidence = build_evidence(
            completed_summary(passed=2, failed=0),
            exported_items([1.0, 1.0]),
            EXPECTED_FIXTURE,
        )

        self.assertEqual(evidence["outcome"], "passed")
        self.assertTrue(evidence["quality_gate"]["passed"])
        self.assertEqual(evidence["quality_gate"]["pass_rate"], 1.0)
        self.assertEqual(
            set(evidence),
            {
                "schema_version",
                "outcome",
                "counts",
                "quality_gate",
                "evaluator",
                "samples",
            },
        )

        serialized = json.dumps(evidence)
        for forbidden in (
            "evalrun-sensitive",
            "item-0",
            "service_identifier",
            "raw service explanation",
            "secret",
            "portal_url",
            "report_url",
            "https://",
        ):
            self.assertNotIn(forbidden, serialized)

    def test_records_quality_breach_without_treating_it_as_operational_failure(self) -> None:
        evidence = build_evidence(
            completed_summary(passed=1, failed=1),
            exported_items([1.0, 0.5]),
            EXPECTED_FIXTURE,
        )

        self.assertEqual(evidence["outcome"], "quality_gate_breached")
        self.assertFalse(evidence["quality_gate"]["passed"])
        self.assertEqual(evidence["quality_gate"]["pass_rate"], 0.5)
        self.assertEqual([sample["passed"] for sample in evidence["samples"]], [True, False])

    def test_rejects_an_operationally_incomplete_run(self) -> None:
        summary = completed_summary(passed=2, failed=0)
        summary["status"] = "failed"

        with self.assertRaises(EvidenceError):
            build_evidence(summary, exported_items([1.0, 1.0]), EXPECTED_FIXTURE)

    def test_rejects_ambiguous_or_missing_sample_results(self) -> None:
        export = exported_items([1.0, 1.0])
        export["items"][0]["results"] = []

        with self.assertRaises(EvidenceError):
            build_evidence(completed_summary(passed=2, failed=0), export, EXPECTED_FIXTURE)

    def test_rejects_a_verdict_that_disagrees_with_the_evaluator_threshold(self) -> None:
        export = exported_items([1.0, 1.0])
        export["items"][0]["results"][0]["passed"] = False

        with self.assertRaises(EvidenceError):
            build_evidence(completed_summary(passed=2, failed=0), export, EXPECTED_FIXTURE)

    def test_rejects_aggregate_counts_that_disagree_with_samples(self) -> None:
        with self.assertRaises(EvidenceError):
            build_evidence(
                completed_summary(passed=2, failed=0),
                exported_items([1.0, 0.5]),
                EXPECTED_FIXTURE,
            )


class WriteEvidenceTests(unittest.TestCase):
    def test_writes_the_reviewed_fixture_and_safe_document(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            summary_path = root / "summary.json"
            export_path = root / "export.json"
            fixture_path = root / "golden.jsonl"
            output_path = root / "artifact" / "results.json"
            summary_path.write_text(
                json.dumps(completed_summary(passed=2, failed=0)), encoding="utf-8"
            )
            export_path.write_text(
                json.dumps(exported_items([1.0, 1.0])), encoding="utf-8"
            )
            fixture_path.write_text(
                "".join(json.dumps(row) + "\n" for row in EXPECTED_FIXTURE),
                encoding="utf-8",
            )

            write_evidence(summary_path, export_path, fixture_path, output_path)

            self.assertTrue(output_path.is_file())
            self.assertEqual(json.loads(output_path.read_text(encoding="utf-8"))["outcome"], "passed")

    def test_rejects_fixture_drift(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            summary_path = root / "summary.json"
            export_path = root / "export.json"
            fixture_path = root / "golden.jsonl"
            output_path = root / "results.json"
            summary_path.write_text(
                json.dumps(completed_summary(passed=2, failed=0)), encoding="utf-8"
            )
            export_path.write_text(
                json.dumps(exported_items([1.0, 1.0])), encoding="utf-8"
            )
            changed = [*EXPECTED_FIXTURE, {"query": "extra", "response": "x", "ground_truth": "x"}]
            fixture_path.write_text(
                "".join(json.dumps(row) + "\n" for row in changed),
                encoding="utf-8",
            )

            with self.assertRaises(EvidenceError):
                write_evidence(summary_path, export_path, fixture_path, output_path)


class WorkflowContractTests(unittest.TestCase):
    def test_live_job_passes_required_azd_environment_inputs(self) -> None:
        workflow = (
            REPOSITORY_ROOT / ".github" / "workflows" / "eval-scenario-ci.yml"
        ).read_text(encoding="utf-8")

        for expected in (
            "AZURE_AI_PROJECT_ENDPOINT: ${{ secrets.AZURE_AI_PROJECT_ENDPOINT }}",
            "AZURE_LOCATION: ${{ vars.AZURE_LOCATION }}",
            "AZURE_RESOURCE_GROUP: ${{ vars.AZURE_RESOURCE_GROUP }}",
            "AZURE_SUBSCRIPTION_ID: ${{ secrets.AZURE_SUBSCRIPTION_ID }}",
            '"AZURE_AI_PROJECT_ENDPOINT",',
            '"AZURE_LOCATION",',
            '"AZURE_RESOURCE_GROUP",',
            '"AZURE_SUBSCRIPTION_ID"',
        ):
            self.assertIn(expected, workflow)

    def test_fixture_contains_only_the_inert_reconciliation_marker(self) -> None:
        marker = (
            REPOSITORY_ROOT
            / "eng"
            / "scripts"
            / "eval-scenario-ci"
            / "fixture"
            / "infra"
            / "main.bicep"
        ).read_text(encoding="utf-8")

        self.assertEqual(
            marker,
            """targetScope = 'resourceGroup'

resource reconciliationMarker 'Microsoft.Resources/deployments@2022-09-01' = {
  name: 'azd-eval-hero-reconciliation'
  properties: {
    mode: 'Incremental'
    template: {
      '$schema': 'https://schema.management.azure.com/schemas/2019-04-01/deploymentTemplate.json#'
      contentVersion: '1.0.0.0'
      resources: []
    }
  }
}
""",
        )


if __name__ == "__main__":
    unittest.main()
