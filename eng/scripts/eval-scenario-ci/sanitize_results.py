#!/usr/bin/env python3

import argparse
import json
import math
import os
import sys
from pathlib import Path
from typing import Any

EVALUATOR = "builtin.f1_score"
EXPECTED_FIXTURE = [
    {
        "query": "What is the capital of France?",
        "response": "Paris",
        "ground_truth": "Paris",
    },
    {
        "query": "What is 2 + 2?",
        "response": "4",
        "ground_truth": "4",
    },
]


class EvidenceError(ValueError):
    pass


def _unique_object(pairs: list[tuple[str, Any]]) -> dict[str, Any]:
    result: dict[str, Any] = {}
    for key, value in pairs:
        if key in result:
            raise EvidenceError("JSON contains a duplicate property.")
        result[key] = value
    return result


def _invalid_constant(_: str) -> Any:
    raise EvidenceError("JSON contains a non-finite number.")


def _load_json(path: Path) -> Any:
    try:
        with path.open(encoding="utf-8") as stream:
            return json.load(
                stream, object_pairs_hook=_unique_object, parse_constant=_invalid_constant
            )
    except (OSError, UnicodeError, json.JSONDecodeError) as error:
        raise EvidenceError("An evidence input is not valid JSON.") from error


def _load_fixture(path: Path) -> list[dict[str, str]]:
    rows: list[dict[str, str]] = []
    try:
        with path.open(encoding="utf-8") as stream:
            for line_number, line in enumerate(stream, start=1):
                if not line.strip():
                    continue
                try:
                    row = json.loads(
                        line, object_pairs_hook=_unique_object, parse_constant=_invalid_constant
                    )
                except json.JSONDecodeError as error:
                    raise EvidenceError(
                        f"The fixture row at line {line_number} is not valid JSON."
                    ) from error
                if not isinstance(row, dict):
                    raise EvidenceError(
                        f"The fixture row at line {line_number} is not an object."
                    )
                rows.append(row)
    except (OSError, UnicodeError) as error:
        raise EvidenceError("The fixture could not be read.") from error

    if rows != EXPECTED_FIXTURE:
        raise EvidenceError("The fixture does not match the reviewed Scenario 5 rows.")
    return rows


def _required_int(source: dict[str, Any], name: str) -> int:
    value = source.get(name)
    if isinstance(value, bool) or not isinstance(value, int) or value < 0:
        raise EvidenceError(f"result_counts.{name} must be a non-negative integer.")
    return value


def _run_counts(summary: Any) -> dict[str, int]:
    if not isinstance(summary, dict) or summary.get("status") != "completed":
        raise EvidenceError("The run summary is not a completed evaluation.")
    counts = summary.get("result_counts")
    if not isinstance(counts, dict):
        raise EvidenceError("The run summary does not contain result_counts.")

    result = {
        name: _required_int(counts, name)
        for name in ("total", "passed", "failed", "errored", "skipped")
    }
    if result["total"] < sum(result[name] for name in ("passed", "failed", "errored", "skipped")):
        raise EvidenceError("The run summary contains inconsistent result counts.")
    return result


def _result_for_sample(item: dict[str, Any], evaluator_threshold: float) -> dict[str, Any]:
    item_status = item.get("status")
    if item_status is not None and not isinstance(item_status, str):
        raise EvidenceError("An exported item contains invalid status metadata.")
    item_status = item_status.strip().lower() if item_status is not None else ""
    if item_status in {"errored", "skipped"}:
        raise EvidenceError("An exported item reports an error or skip.")

    results = item.get("results")
    if not isinstance(results, list):
        raise EvidenceError("An exported item does not contain evaluator results.")

    candidates = [
        result
        for result in results
        if isinstance(result, dict)
        and (result.get("name") in {EVALUATOR, "f1_score"} or result.get("metric") == "f1_score")
    ]
    if len(candidates) != 1:
        raise EvidenceError("An exported item does not contain one F1 result.")

    result = candidates[0]
    signals: dict[str, str] = {}
    for field in ("status", "label"):
        value = result.get(field)
        if value is not None and not isinstance(value, str):
            raise EvidenceError("An F1 result contains invalid outcome metadata.")
        signals[field] = value.strip().lower() if value is not None else ""

    sample = result.get("sample")
    has_sample_error = False
    if sample is not None:
        if not isinstance(sample, dict):
            raise EvidenceError("An F1 result contains invalid sample metadata.")
        error = sample.get("error")
        if error is not None:
            if not isinstance(error, dict):
                raise EvidenceError("An F1 result contains invalid sample error metadata.")
            for field in ("code", "message"):
                value = error.get(field)
                if value is not None and not isinstance(value, str):
                    raise EvidenceError("An F1 result contains invalid sample error metadata.")
                has_sample_error = has_sample_error or bool(value)

    if (
        signals["status"] in {"error", "errored", "skipped"}
        or signals["label"] == "skipped"
        or has_sample_error
    ):
        raise EvidenceError("An F1 result reports an evaluator error or skip.")

    score = result.get("score")
    if isinstance(score, bool) or not isinstance(score, (int, float, str)):
        raise EvidenceError("An F1 result does not contain a numeric score.")
    try:
        score = float(score)
    except (ValueError, OverflowError) as error:
        raise EvidenceError("An F1 result does not contain a numeric score.") from error
    if not math.isfinite(score) or score < 0 or score > 1:
        raise EvidenceError("An F1 result contains an invalid score.")

    passed = result.get("passed")
    if not isinstance(passed, bool):
        raise EvidenceError("An F1 result does not contain a pass verdict.")
    if passed and signals["label"] in {"fail", "failed"}:
        raise EvidenceError("An F1 result has a failure label that contradicts its pass verdict.")
    if passed != (score >= evaluator_threshold):
        raise EvidenceError("An F1 result is inconsistent with the configured threshold.")
    if item_status in {"passed", "failed"} and (item_status == "passed") != passed:
        raise EvidenceError("An exported item status contradicts its F1 verdict.")

    return {"score": score, "passed": passed}


def _samples(
    export: Any, fixture: list[dict[str, str]], evaluator_threshold: float
) -> list[dict[str, Any]]:
    if not isinstance(export, dict) or not isinstance(export.get("items"), list):
        raise EvidenceError("The export does not contain an items array.")
    items = export["items"]
    if len(items) != len(fixture):
        raise EvidenceError("The export does not contain one item per fixture row.")

    fields = ("query", "response", "ground_truth")
    indexed: dict[tuple[str, ...], dict[str, Any]] = {}
    for item in items:
        if not isinstance(item, dict) or not isinstance(item.get("datasource_item"), dict):
            raise EvidenceError("An exported item does not contain a fixture row.")
        row = item["datasource_item"]
        if any(not isinstance(row.get(field), str) for field in fields):
            raise EvidenceError("An exported fixture field is not a string.")
        key = tuple(row[field] for field in fields)
        if key in indexed:
            raise EvidenceError("The export contains duplicate fixture rows.")
        indexed[key] = item

    samples: list[dict[str, Any]] = []
    for expected in fixture:
        item = indexed.pop(tuple(expected[field] for field in fields), None)
        if item is None:
            raise EvidenceError("An exported item could not be matched to one fixture row.")
        samples.append({**expected, **_result_for_sample(item, evaluator_threshold)})
    return samples


def _validate_threshold(value: float) -> None:
    if isinstance(value, bool) or not isinstance(value, (int, float)):
        raise EvidenceError("Thresholds must be finite numbers between zero and one.")
    if not math.isfinite(value) or not 0 <= value <= 1:
        raise EvidenceError("Thresholds must be finite numbers between zero and one.")


def build_evidence(
    summary: Any,
    export: Any,
    fixture: list[dict[str, str]],
    *,
    evaluator_threshold: float,
    quality_gate_threshold: float,
) -> dict[str, Any]:
    _validate_threshold(evaluator_threshold)
    _validate_threshold(quality_gate_threshold)
    counts = _run_counts(summary)
    samples = _samples(export, fixture, evaluator_threshold)
    sample_passed = sum(sample["passed"] for sample in samples)
    sample_failed = len(samples) - sample_passed
    if (
        counts["total"] != len(samples)
        or counts["passed"] != sample_passed
        or counts["failed"] != sample_failed
        or counts["errored"] != 0
        or counts["skipped"] != 0
    ):
        raise EvidenceError(
            "The aggregate result counts do not match the sanitized sample verdicts."
        )

    pass_rate = counts["passed"] / counts["total"] if counts["total"] else 0.0
    quality_passed = counts["total"] > 0 and pass_rate >= quality_gate_threshold

    return {
        "schema_version": 1,
        "outcome": "passed" if quality_passed else "quality_gate_breached",
        "counts": counts,
        "quality_gate": {
            "metric": "pass-rate",
            "threshold": quality_gate_threshold,
            "pass_rate": pass_rate,
            "passed": quality_passed,
        },
        "evaluator": {
            "name": EVALUATOR,
            "threshold": evaluator_threshold,
        },
        "samples": samples,
    }


def write_evidence(
    summary_path: Path,
    export_path: Path,
    fixture_path: Path,
    output_path: Path,
    *,
    evaluator_threshold: float,
    quality_gate_threshold: float,
) -> dict[str, Any]:
    evidence = build_evidence(
        _load_json(summary_path),
        _load_json(export_path),
        _load_fixture(fixture_path),
        evaluator_threshold=evaluator_threshold,
        quality_gate_threshold=quality_gate_threshold,
    )
    output_path.parent.mkdir(parents=True, exist_ok=True)
    temporary = output_path.with_suffix(output_path.suffix + ".tmp")
    try:
        with temporary.open("w", encoding="utf-8", newline="\n") as stream:
            json.dump(evidence, stream, indent=2, sort_keys=True)
            stream.write("\n")
        os.replace(temporary, output_path)
        try:
            output_path.chmod(0o600)
        except OSError:
            pass
    finally:
        temporary.unlink(missing_ok=True)
    return evidence


def main() -> int:
    parser = argparse.ArgumentParser(
        description="Create an allowlisted CI artifact from evaluation output."
    )
    parser.add_argument("--run-summary", type=Path, required=True)
    parser.add_argument("--export", type=Path, required=True)
    parser.add_argument("--fixture", type=Path, required=True)
    parser.add_argument("--output", type=Path, required=True)
    parser.add_argument("--evaluator-threshold", type=float, required=True)
    parser.add_argument("--quality-gate-threshold", type=float, required=True)
    args = parser.parse_args()

    try:
        evidence = write_evidence(
            args.run_summary,
            args.export,
            args.fixture,
            args.output,
            evaluator_threshold=args.evaluator_threshold,
            quality_gate_threshold=args.quality_gate_threshold,
        )
    except EvidenceError as error:
        print(f"error: {error}", file=sys.stderr)
        return 1

    print(f"Sanitized evidence created with outcome: {evidence['outcome']}.")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
