#!/usr/bin/env python3

import argparse
import json
import math
import os
import sys
from pathlib import Path
from typing import Any

EVALUATOR = "builtin.f1_score"
EVALUATOR_THRESHOLD = 0.9
QUALITY_GATE_THRESHOLD = 0.8
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


def _load_json(path: Path) -> Any:
    try:
        with path.open(encoding="utf-8") as stream:
            return json.load(stream, object_pairs_hook=_unique_object)
    except (OSError, json.JSONDecodeError) as error:
        raise EvidenceError(f"{path.name} is not valid JSON.") from error


def _load_fixture(path: Path) -> list[dict[str, str]]:
    rows: list[dict[str, str]] = []
    try:
        with path.open(encoding="utf-8") as stream:
            for line_number, line in enumerate(stream, start=1):
                if not line.strip():
                    continue
                try:
                    row = json.loads(line, object_pairs_hook=_unique_object)
                except json.JSONDecodeError as error:
                    raise EvidenceError(
                        f"The fixture row at line {line_number} is not valid JSON."
                    ) from error
                if not isinstance(row, dict):
                    raise EvidenceError(
                        f"The fixture row at line {line_number} is not an object."
                    )
                rows.append(row)
    except OSError as error:
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
    if result["total"] < result["passed"] + result["failed"]:
        raise EvidenceError("The run summary contains inconsistent result counts.")
    return result


def _result_for_sample(item: dict[str, Any]) -> dict[str, Any]:
    results = item.get("results")
    if not isinstance(results, list):
        raise EvidenceError("An exported item does not contain evaluator results.")

    candidates = [
        result
        for result in results
        if isinstance(result, dict)
        and (result.get("name") in {EVALUATOR, "f1_score"} or result.get("metric") == "f1_score")
    ]
    if not candidates and len(results) == 1 and isinstance(results[0], dict):
        candidates = [results[0]]
    if len(candidates) != 1:
        raise EvidenceError("An exported item does not contain one F1 result.")

    result = candidates[0]
    score = result.get("score")
    if isinstance(score, bool) or not isinstance(score, (int, float)):
        raise EvidenceError("An F1 result does not contain a numeric score.")
    score = float(score)
    if not math.isfinite(score) or score < 0 or score > 1:
        raise EvidenceError("An F1 result contains an invalid score.")

    passed = result.get("passed")
    if not isinstance(passed, bool):
        raise EvidenceError("An F1 result does not contain a pass verdict.")
    if passed != (score >= EVALUATOR_THRESHOLD):
        raise EvidenceError("An F1 result is inconsistent with the configured threshold.")

    return {"score": score, "passed": passed}


def _samples(export: Any, fixture: list[dict[str, str]]) -> list[dict[str, Any]]:
    if not isinstance(export, dict) or not isinstance(export.get("items"), list):
        raise EvidenceError("The export does not contain an items array.")
    items = export["items"]
    if len(items) != len(fixture):
        raise EvidenceError("The export does not contain one item per fixture row.")

    samples: list[dict[str, Any]] = []
    used: set[int] = set()
    for expected in fixture:
        matches = [
            index
            for index, item in enumerate(items)
            if isinstance(item, dict)
            and isinstance(item.get("datasource_item"), dict)
            and all(item["datasource_item"].get(key) == value for key, value in expected.items())
        ]
        if len(matches) != 1 or matches[0] in used:
            raise EvidenceError("An exported item could not be matched to one fixture row.")
        index = matches[0]
        used.add(index)
        verdict = _result_for_sample(items[index])
        samples.append({**expected, **verdict})
    return samples


def build_evidence(summary: Any, export: Any, fixture: list[dict[str, str]]) -> dict[str, Any]:
    counts = _run_counts(summary)
    samples = _samples(export, fixture)
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

    scored = counts["passed"] + counts["failed"]
    pass_rate = counts["passed"] / scored if scored else 0.0
    quality_passed = counts["total"] > 0 and scored > 0 and pass_rate >= QUALITY_GATE_THRESHOLD

    return {
        "schema_version": 1,
        "outcome": "passed" if quality_passed else "quality_gate_breached",
        "counts": counts,
        "quality_gate": {
            "metric": "pass-rate",
            "threshold": QUALITY_GATE_THRESHOLD,
            "pass_rate": pass_rate,
            "passed": quality_passed,
        },
        "evaluator": {
            "name": EVALUATOR,
            "threshold": EVALUATOR_THRESHOLD,
        },
        "samples": samples,
    }


def write_evidence(
    summary_path: Path, export_path: Path, fixture_path: Path, output_path: Path
) -> dict[str, Any]:
    evidence = build_evidence(
        _load_json(summary_path),
        _load_json(export_path),
        _load_fixture(fixture_path),
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
    args = parser.parse_args()

    try:
        evidence = write_evidence(
            args.run_summary, args.export, args.fixture, args.output
        )
    except EvidenceError as error:
        print(f"error: {error}", file=sys.stderr)
        return 1

    print(f"Sanitized evidence created with outcome: {evidence['outcome']}.")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
