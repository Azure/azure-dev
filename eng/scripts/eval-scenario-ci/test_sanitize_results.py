import json
import re
import subprocess
import sys
import tempfile
import unittest
from functools import partial
from pathlib import Path

sys.path.insert(0, str(Path(__file__).parent))

from sanitize_results import (
    EXPECTED_FIXTURE,
    EvidenceError,
    build_evidence as _build_evidence,
    write_evidence as _write_evidence,
)

REPOSITORY_ROOT = Path(__file__).resolve().parents[3]
SCRIPT_ROOT = Path(__file__).resolve().parent
THRESHOLDS = {
    "evaluator_threshold": json.loads(
        (SCRIPT_ROOT / "fixture" / "live" / "f1-parameters.json").read_text(encoding="utf-8")
    )["threshold"],
    "quality_gate_threshold": 0.8,
}
build_evidence = partial(_build_evidence, **THRESHOLDS)
write_evidence = partial(_write_evidence, **THRESHOLDS)


def completed_summary(*, passed: int, failed: int, errored: int = 0, skipped: int = 0) -> dict:
    return {
        "id": "evalrun-sensitive",
        "status": "completed",
        "report_url": "https://user:password@example.test/report?sig=secret",
        "result_counts": {
            "total": passed + failed + errored + skipped,
            "passed": passed,
            "failed": failed,
            "errored": errored,
            "skipped": skipped,
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
                        "passed": score >= THRESHOLDS["evaluator_threshold"],
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

    def test_rejects_a_single_differently_named_or_unidentified_evaluator(self) -> None:
        for name in ("builtin.relevance", "relevance", None):
            with self.subTest(name=name):
                export = exported_items([1.0, 1.0])
                for item in export["items"]:
                    item["results"][0]["name"] = name
                with self.assertRaisesRegex(EvidenceError, "one F1 result"):
                    build_evidence(completed_summary(passed=2, failed=0), export, EXPECTED_FIXTURE)

    def test_accepts_explicit_f1_names_and_metric(self) -> None:
        for identity in ({"name": "builtin.f1_score"}, {"name": "f1_score"}, {"metric": "f1_score"}):
            with self.subTest(identity=identity):
                export = exported_items([1.0, 1.0])
                for item in export["items"]:
                    result = item["results"][0]
                    del result["name"]
                    result.update(identity)
                evidence = build_evidence(
                    completed_summary(passed=2, failed=0), export, EXPECTED_FIXTURE
                )
                self.assertEqual(evidence["evaluator"]["name"], "builtin.f1_score")
                self.assertEqual(evidence["outcome"], "passed")

    def test_accepts_quoted_numeric_scores_and_emits_numeric_artifact_scores(self) -> None:
        for scores in (("1.0", "0.5"), ("9e-1", "0"), (" 1.0 ", 0.5)):
            with self.subTest(scores=scores):
                export = exported_items([1.0, 0.5])
                for item, score in zip(export["items"], scores, strict=True):
                    item["results"][0]["score"] = score
                evidence = build_evidence(
                    completed_summary(passed=1, failed=1), export, EXPECTED_FIXTURE
                )
                serialized = json.loads(json.dumps(evidence))
                self.assertEqual(
                    [sample["score"] for sample in serialized["samples"]],
                    [float(score) for score in scores],
                )
                for sample in serialized["samples"]:
                    self.assertIsInstance(sample["score"], float)
                self.assertEqual(evidence["outcome"], "quality_gate_breached")

    def test_rejects_invalid_scores_without_disclosing_raw_values(self) -> None:
        for score in (
            True, False, None, [], {}, "", "service-secret", "NaN", "Infinity",
            "-Infinity", "1e999", "-0.1", "1.1", float("nan"), float("inf"),
            -0.1, 1.1, 10**400,
        ):
            with self.subTest(score=score):
                export = exported_items([1.0, 1.0])
                export["items"][0]["results"][0]["score"] = score
                with self.assertRaisesRegex(EvidenceError, "score") as caught:
                    build_evidence(completed_summary(passed=2, failed=0), export, EXPECTED_FIXTURE)
                self.assertNotIn("service-secret", str(caught.exception))

    def test_evaluator_error_and_skip_signals_override_pass_and_fail_verdicts(self) -> None:
        for signal in (
            {"status": "error"}, {"status": "errored"}, {"status": " ERROR "},
            {"status": "skipped"}, {"status": " SkIpPeD "}, {"label": "skipped"},
            {"label": " SKIPPED "}, {"sample": {"error": {"code": "FAILED_EXECUTION"}}},
            {"sample": {"error": {"message": "service-secret"}}},
        ):
            for passed in (True, False):
                with self.subTest(signal=signal, passed=passed):
                    export = exported_items([1.0, 1.0 if passed else 0.0])
                    export["items"][1]["results"][0].update(signal)
                    with self.assertRaisesRegex(EvidenceError, "evaluator error or skip") as caught:
                        build_evidence(
                            completed_summary(passed=2 if passed else 1, failed=0 if passed else 1),
                            export, EXPECTED_FIXTURE,
                        )
                    self.assertNotIn("service-secret", str(caught.exception))

    def test_rejects_malformed_evaluator_outcome_metadata(self) -> None:
        for signal in (
            {"status": []}, {"label": {}}, {"sample": False}, {"sample": []},
            {"sample": {"error": "service-secret"}}, {"sample": {"error": []}},
            {"sample": {"error": {"code": False}}},
            {"sample": {"error": {"message": ["service-secret"]}}},
        ):
            with self.subTest(signal=signal):
                export = exported_items([1.0, 1.0])
                export["items"][0]["results"][0].update(signal)
                with self.assertRaisesRegex(EvidenceError, "invalid .*metadata") as caught:
                    build_evidence(completed_summary(passed=2, failed=0), export, EXPECTED_FIXTURE)
                self.assertNotIn("service-secret", str(caught.exception))

    def test_accepts_empty_error_metadata_and_completed_evaluator_status(self) -> None:
        for signal in (
            {"status": "completed", "label": "passed"}, {"status": None, "label": None},
            {"sample": None}, {"sample": {}}, {"sample": {"error": None}},
            {"sample": {"error": {}}}, {"sample": {"error": {"code": "", "message": ""}}},
            {"sample": {"error": {"code": None, "message": None}}},
        ):
            with self.subTest(signal=signal):
                export = exported_items([1.0, 1.0])
                export["items"][0]["results"][0].update(signal)
                evidence = build_evidence(
                    completed_summary(passed=2, failed=0), export, EXPECTED_FIXTURE
                )
                self.assertEqual(evidence["outcome"], "passed")

    def test_item_error_and_skip_statuses_override_pass_and_fail_verdicts(self) -> None:
        for status in ("errored", " ERRORED ", "skipped", " SkIpPeD "):
            for passed in (True, False):
                with self.subTest(status=status, passed=passed):
                    export = exported_items([1.0, 1.0 if passed else 0.0])
                    export["items"][1]["status"] = status
                    with self.assertRaisesRegex(EvidenceError, "item reports an error or skip"):
                        build_evidence(
                            completed_summary(passed=2 if passed else 1, failed=0 if passed else 1),
                            export, EXPECTED_FIXTURE,
                        )

    def test_rejects_invalid_item_status_metadata(self) -> None:
        for status in (True, False, [], {}, 1):
            with self.subTest(status=status):
                export = exported_items([1.0, 1.0])
                export["items"][0]["status"] = status
                with self.assertRaisesRegex(EvidenceError, "invalid status metadata"):
                    build_evidence(completed_summary(passed=2, failed=0), export, EXPECTED_FIXTURE)

    def test_item_canonical_statuses_must_agree_with_sample_verdicts(self) -> None:
        for status, passed in (("failed", True), ("passed", False)):
            with self.subTest(status=status):
                export = exported_items([1.0, 1.0 if passed else 0.0])
                export["items"][1]["status"] = status
                with self.assertRaisesRegex(EvidenceError, "item status contradicts"):
                    build_evidence(
                        completed_summary(passed=2 if passed else 1, failed=0 if passed else 1),
                        export, EXPECTED_FIXTURE,
                    )

    def test_accepts_lifecycle_and_matching_canonical_item_statuses(self) -> None:
        for passed in (True, False):
            for status in (None, "", "completed", " PASSED " if passed else " FAILED "):
                with self.subTest(status=status, passed=passed):
                    export = exported_items([1.0, 1.0 if passed else 0.0])
                    export["items"][1]["status"] = status
                    evidence = build_evidence(
                        completed_summary(passed=2 if passed else 1, failed=0 if passed else 1),
                        export, EXPECTED_FIXTURE,
                    )
                    self.assertEqual(
                        evidence["outcome"], "passed" if passed else "quality_gate_breached"
                    )

    def test_failure_labels_override_true_verdicts(self) -> None:
        for label in ("fail", "failed", " FAIL ", " FaIlEd "):
            with self.subTest(label=label):
                export = exported_items([1.0, 1.0])
                export["items"][0]["results"][0]["label"] = label
                with self.assertRaisesRegex(EvidenceError, "failure label"):
                    build_evidence(completed_summary(passed=2, failed=0), export, EXPECTED_FIXTURE)

    def test_failure_labels_preserve_consistent_failed_verdicts(self) -> None:
        for label in ("fail", "failed"):
            with self.subTest(label=label):
                export = exported_items([1.0, 0.0])
                export["items"][1]["results"][0]["label"] = label
                evidence = build_evidence(
                    completed_summary(passed=1, failed=1), export, EXPECTED_FIXTURE
                )
                self.assertEqual(evidence["outcome"], "quality_gate_breached")

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

    def test_counts_include_errored_and_skipped_before_matching_samples(self) -> None:
        for component in ("errored", "skipped"):
            with self.subTest(component=component):
                summary = completed_summary(passed=2, failed=0, **{component: 1})
                summary["result_counts"]["total"] = 2
                with self.assertRaisesRegex(EvidenceError, "inconsistent result counts"):
                    build_evidence(summary, exported_items([1.0, 1.0]), EXPECTED_FIXTURE)

    def test_rejects_operational_outcomes_even_when_the_counts_add_up(self) -> None:
        for component in ("errored", "skipped"):
            with self.subTest(component=component):
                summary = completed_summary(passed=1, failed=0, **{component: 1})
                with self.assertRaisesRegex(EvidenceError, "do not match"):
                    build_evidence(summary, exported_items([1.0, 1.0]), EXPECTED_FIXTURE)

    def test_rejects_missing_or_invalid_counts(self) -> None:
        for component in ("total", "passed", "failed", "errored", "skipped"):
            for invalid in (None, True, -1, 1.5, "2"):
                with self.subTest(component=component, invalid=invalid):
                    summary = completed_summary(passed=2, failed=0)
                    summary["result_counts"][component] = invalid
                    with self.assertRaisesRegex(EvidenceError, "non-negative integer"):
                        build_evidence(summary, exported_items([1.0, 1.0]), EXPECTED_FIXTURE)
            summary = completed_summary(passed=2, failed=0)
            del summary["result_counts"][component]
            with self.subTest(component=component, missing=True):
                with self.assertRaisesRegex(EvidenceError, "non-negative integer"):
                    build_evidence(summary, exported_items([1.0, 1.0]), EXPECTED_FIXTURE)

    def test_matches_reordered_rows_without_changing_fixture_order(self) -> None:
        export = exported_items([1.0, 0.5])
        export["items"].reverse()
        evidence = build_evidence(
            completed_summary(passed=1, failed=1), export, EXPECTED_FIXTURE
        )
        self.assertEqual([row["query"] for row in evidence["samples"]],
                         [row["query"] for row in EXPECTED_FIXTURE])
        self.assertEqual([row["score"] for row in evidence["samples"]], [1.0, 0.5])

    def test_rejects_duplicate_or_unmatched_exported_rows(self) -> None:
        export = exported_items([1.0, 1.0])
        export["items"][1]["datasource_item"] = export["items"][0]["datasource_item"]
        with self.assertRaisesRegex(EvidenceError, "duplicate fixture rows"):
            build_evidence(completed_summary(passed=2, failed=0), export, EXPECTED_FIXTURE)
        export["items"][1]["datasource_item"] = {
            "query": "unexpected", "response": "x", "ground_truth": "x"
        }
        with self.assertRaisesRegex(EvidenceError, "matched to one fixture row"):
            build_evidence(completed_summary(passed=2, failed=0), export, EXPECTED_FIXTURE)

    def test_rejects_unhashable_or_non_string_export_fields(self) -> None:
        for invalid in ({}, [], None, 42):
            with self.subTest(invalid=invalid):
                export = exported_items([1.0, 1.0])
                export["items"][0]["datasource_item"]["query"] = invalid
                with self.assertRaisesRegex(EvidenceError, "not a string"):
                    build_evidence(completed_summary(passed=2, failed=0), export, EXPECTED_FIXTURE)

    def test_uses_explicit_thresholds_in_verdicts_gate_and_artifact(self) -> None:
        export = exported_items([0.7, 0.4])
        export["items"][0]["results"][0]["passed"] = True
        evidence = build_evidence(
            completed_summary(passed=1, failed=1), export, EXPECTED_FIXTURE,
            evaluator_threshold=0.6, quality_gate_threshold=0.5,
        )
        self.assertTrue(evidence["quality_gate"]["passed"])
        self.assertEqual(evidence["quality_gate"]["threshold"], 0.5)
        self.assertEqual(evidence["evaluator"]["threshold"], 0.6)
        breached = build_evidence(
            completed_summary(passed=1, failed=1), export, EXPECTED_FIXTURE,
            evaluator_threshold=0.6, quality_gate_threshold=0.51,
        )
        self.assertFalse(breached["quality_gate"]["passed"])

    def test_rejects_invalid_thresholds(self) -> None:
        for name in THRESHOLDS:
            for invalid in (True, "0.8", -0.1, 1.1, float("nan"), float("inf")):
                with self.subTest(name=name, invalid=invalid):
                    with self.assertRaisesRegex(EvidenceError, "Thresholds must be finite"):
                        build_evidence(
                            completed_summary(passed=2, failed=0),
                            exported_items([1.0, 1.0]), EXPECTED_FIXTURE,
                            **{name: invalid},
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

    def test_fails_closed_at_every_json_and_jsonl_input_boundary(self) -> None:
        invalid_documents = (
            ("summary", '{"status":"completed","status":"failed"}', "duplicate property"),
            ("summary", '{"nested":{"x":1,"x":2}}', "duplicate property"),
            ("export", '{"items":[],"items":[]}', "duplicate property"),
            ("summary", '{"status":', "not valid JSON"),
            ("export", "service-secret-not-json", "not valid JSON"),
            ("summary", '{"unexpected": NaN}', "non-finite number"),
            ("export", '{"unexpected": Infinity}', "non-finite number"),
            ("fixture", '{"query":"x","query":"y"}\n', "duplicate property"),
            ("fixture", '{"query":\n', "line 1 is not valid JSON"),
            ("fixture", "[]\n", "line 1 is not an object"),
            ("fixture", '"service-secret"\n', "line 1 is not an object"),
        )
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            paths = {name: root / f"{name}.json" for name in ("summary", "export", "fixture")}
            output = root / "safe" / "results.json"
            for name, document, message in invalid_documents:
                with self.subTest(input=name, document=document):
                    paths["summary"].write_text(
                        json.dumps(completed_summary(passed=2, failed=0)), encoding="utf-8"
                    )
                    paths["export"].write_text(
                        json.dumps(exported_items([1.0, 1.0])), encoding="utf-8"
                    )
                    paths["fixture"].write_text(
                        "".join(json.dumps(row) + "\n" for row in EXPECTED_FIXTURE),
                        encoding="utf-8",
                    )
                    paths[name].write_text(document, encoding="utf-8")
                    with self.assertRaisesRegex(EvidenceError, message) as caught:
                        write_evidence(paths["summary"], paths["export"], paths["fixture"], output)
                    self.assertNotIn("service-secret", str(caught.exception))
                    self.assertFalse(output.exists())
                    self.assertFalse(output.with_suffix(".json.tmp").exists())

    def test_cli_rejects_malformed_evidence_without_disclosing_it(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            malformed = root / "malformed.json"
            malformed.write_text("https://user:secret@example.test/?sig=sas-secret", encoding="utf-8")
            output = root / "results.json"
            result = subprocess.run(
                [sys.executable, str(SCRIPT_ROOT / "sanitize_results.py"),
                 "--run-summary", str(malformed), "--export", str(malformed),
                 "--fixture", str(malformed), "--output", str(output),
                 "--evaluator-threshold", "0.9", "--quality-gate-threshold", "0.8"],
                capture_output=True, text=True, check=False, timeout=15,
            )
            self.assertEqual(result.returncode, 1)
            self.assertIn("not valid JSON", result.stderr)
            self.assertNotIn("secret", result.stdout + result.stderr)
            self.assertNotIn("example.test", result.stdout + result.stderr)
            self.assertFalse(output.exists())


class WorkflowContractTests(unittest.TestCase):
    def _assert_production_registry(self, surface: str) -> None:
        for pattern in (
            r"\bextension\s+source\s+add\b",
            r"--source(?:\s+|=)(?!azd(?:\s|$))\S+",
            r"--version\b",
            r"registry\.json\b",
            r"githubusercontent\.com\b",
        ):
            self.assertNotRegex(surface, re.compile(pattern, re.IGNORECASE))
        for url in re.findall(r"https?://[^\s\"'<>]+", surface, flags=re.IGNORECASE):
            self.assertEqual(url, "https://aka.ms/azd/extensions/registry")

    def test_github_actions_has_no_live_authentication_surface(self) -> None:
        workflow = (
            REPOSITORY_ROOT / ".github" / "workflows" / "eval-scenario-ci.yml"
        ).read_text(encoding="utf-8")

        for forbidden in ("secrets.", "id-token:", "run_live", "auth login", "AZURE_SUBSCRIPTION_ID",
                          "AZURE_AI_PROJECT_ENDPOINT", "live-scenario:"):
            self.assertNotIn(forbidden, workflow)
        self.assertIn("version: latest", workflow)
        self.assertIn("contents: read", workflow)
        self.assertIn("ubuntu-24.04", workflow)
        self.assertIn("windows-2025", workflow)

    def test_both_ci_surfaces_share_canonical_latest_install_and_verification(self) -> None:
        workflow = (
            REPOSITORY_ROOT / ".github" / "workflows" / "eval-scenario-ci.yml"
        ).read_text(encoding="utf-8")
        pipeline = (
            REPOSITORY_ROOT / "eng" / "pipelines" / "eval-scenario-live.yml"
        ).read_text(encoding="utf-8")
        installer = (SCRIPT_ROOT / "install_packages.ps1").read_text(encoding="utf-8")
        for surface in (workflow, pipeline):
            self.assertIn("eng/scripts/eval-scenario-ci/install_packages.ps1", surface)
            self.assertNotIn("azd extension install", surface)
        for surface in (workflow, pipeline, installer):
            for forbidden in (
                "AZD_VERSION", "AZD_REGISTRY_URL", "AZD_EVALUATIONS_VERSION", "AZD_DATASET_VERSION",
            ):
                self.assertNotIn(forbidden, surface)
            self._assert_production_registry(surface)
        self.assertIn("version: latest", workflow)
        self.assertIn("Version: latest", pipeline)
        self.assertIn("azd extension install $extensionId --source azd --no-prompt", installer)
        self.assertIn("$package.installedVersion -cne $package.version", installer)

    def test_ado_live_gate_is_manual_opt_in_internal_and_main_only(self) -> None:
        pipeline = (
            REPOSITORY_ROOT / "eng" / "pipelines" / "eval-scenario-live.yml"
        ).read_text(encoding="utf-8")
        for expected in (
            "trigger: none", "pr: none", "default: false",
            "${{ parameters.runLive }}",
            "eq(variables['System.TeamProject'], 'internal')",
            "eq(variables['Build.Reason'], 'Manual')",
            "eq(variables['Build.SourceBranch'], 'refs/heads/main')",
            "template: /eng/pipelines/templates/stages/1es-redirect.yml",
            "task: AzureCLI@2", "keepAzSessionActive: true", "visibleAzLogin: false",
            "output: pipelineArtifact",
            "eq(variables['EvalScenarioArtifactReady'], 'true')",
        ):
            self.assertIn(expected, pipeline)

    def test_live_runner_uses_the_fixture_threshold_and_one_quality_gate_value(self) -> None:
        runner = (SCRIPT_ROOT / "run_live.ps1").read_text(encoding="utf-8")
        config = (SCRIPT_ROOT / "fixture" / "live" / "azure.eval.yaml").read_text(encoding="utf-8")
        self.assertIn("$ref: ./f1-parameters.json", config)
        self.assertIn('"fixture/live/f1-parameters.json"', runner)
        self.assertIn("$evaluatorThreshold = [string] $evaluatorParameters.threshold", runner)
        self.assertIn('--fail-on "pass-rate=$qualityGateThreshold"', runner)
        self.assertIn("--evaluator-threshold $evaluatorThreshold", runner)
        self.assertIn("--quality-gate-threshold $qualityGateThreshold", runner)
        self.assertEqual(runner.count('$qualityGateThreshold = "0.8"'), 1)

    def test_live_fixture_uses_current_configuration_keys_and_dataset_columns(self) -> None:
        config = (SCRIPT_ROOT / "fixture" / "live" / "azure.eval.yaml").read_text(encoding="utf-8")
        self.assertEqual(config, """datasets:
  - name: ci-golden
    file: ./data/golden.jsonl

evals:
  - name: ci-f1-quality
    dataset: ci-golden
    evaluationLevel: turn
    evaluators:
      - evaluator: builtin.f1_score
        initializationParameters:
          $ref: ./f1-parameters.json
        dataMapping:
          query: "{{item.query}}"
          response: "{{item.response}}"
          ground_truth: "{{item.ground_truth}}"
""")

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
