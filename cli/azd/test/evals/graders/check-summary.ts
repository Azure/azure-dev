// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
//
// Standalone `program` grader, wired up by the `program` grader in
// eval.yaml. 
//
// vally sets two environment variables before spawning this process:
//   EVALUATE_WORKSPACE    - path to the workspace root
//   EVALUATE_GRADER_INPUT - path to a JSON file containing the GraderInput
//
// We print a GraderResult, as JSON, to stdout, which vally then picks up and
// parses to produce a result. If you need to do diagnostic output, use stderr.
//
// Docs: https://aka.ms/vally -> Reference -> Graders -> program

import { existsSync, readFileSync } from "node:fs";
import { join } from "node:path";

import type { GraderInput, GraderResult } from "@microsoft/vally/graders";

function subCheck(name: string, passed: boolean, evidence: string): GraderResult {
  return { name, kind: "code", passed, score: passed ? 1 : 0, evidence };
}

function checkSummaryFile(workspace: string): GraderResult[] {
  const summaryPath = join(workspace, "notes-summary.md");
  const checks: GraderResult[] = [];

  if (!existsSync(summaryPath)) {
    checks.push(subCheck("summary-file-exists", false, `${summaryPath} does not exist`));
    return checks;
  }
  checks.push(subCheck("summary-file-exists", true, `${summaryPath} exists`));

  const lines = readFileSync(summaryPath, "utf-8")
    .split(/\r?\n/)
    .filter((line) => line.trim().length > 0);

  // Rudimentary structural check: the first non-blank line must report a
  // "Total notes: <N>" header. Unlike the text-matching graders in
  // eval.yaml (which inspect the agent's chat output), this one verifies
  // the *workspace file* has the right shape and internal consistency,
  // which a plain string match can't do.
  const headerMatch = /^Total notes:\s*(\d+)\s*$/.exec(lines[0] ?? "");
  checks.push(
    subCheck(
      "summary-has-total-line",
      headerMatch != null,
      headerMatch != null
        ? `First line reports "${lines[0]}"`
        : `Expected first line to match "Total notes: <N>", got ${JSON.stringify(lines[0] ?? "")}`,
    ),
  );

  // The bullet count must actually match the reported total -- catches an
  // agent that reports the right number but lists the wrong count of notes.
  const bulletCount = lines.slice(1).filter((line) => /^[-*]\s+\S/.test(line)).length;
  const reportedTotal = headerMatch ? Number(headerMatch[1]) : undefined;
  checks.push(
    subCheck(
      "bullet-count-matches-total",
      reportedTotal !== undefined && bulletCount === reportedTotal,
      `Found ${bulletCount} bullet line(s); header reported ${reportedTotal ?? "<none>"}`,
    ),
  );

  return checks;
}

function checkAgentUsedTools(input: GraderInput): GraderResult {
  const toolCallCount = input.trajectory?.metrics?.toolCallCount ?? 0;
  return subCheck(
    "agent-used-tools",
    toolCallCount > 0,
    `Trajectory recorded ${toolCallCount} tool call(s)`,
  );
}

function main(): GraderResult {
  const workspace = process.env["EVALUATE_WORKSPACE"];
  const graderInputPath = process.env["EVALUATE_GRADER_INPUT"];
  if (!workspace || !graderInputPath) {
    throw new Error("EVALUATE_WORKSPACE / EVALUATE_GRADER_INPUT were not set");
  }

  // Program graders are just simple programs - GraderInput comes in on stdin, as JSON, and 
  // GraderOutput (our results) goes on stdout.
  const input = JSON.parse(readFileSync(graderInputPath, "utf-8")) as GraderInput;
  const details = [
    ...checkSummaryFile(workspace), 
    checkAgentUsedTools(input)
  ];

  const passed = details.every((d) => d.passed);
  const score = details.filter((d) => d.passed).length / details.length;

  return {
    name: "fixture-summary-check",
    kind: "code",
    passed,
    score,
    evidence: passed
      ? `All ${details.length} checks passed`
      : `${details.filter((d) => !d.passed).length} of ${details.length} check(s) failed`,
    details,
  };
}

try {
  process.stdout.write(JSON.stringify(main()));
} catch (err: unknown) {
  // Never crash without a result: emit a failing GraderResult instead of an
  // uncaught exception, so a bug in this script reads as a failed grader
  // rather than an opaque non-zero exit code.
  const result: GraderResult = {
    name: "fixture-summary-check",
    kind: "code",
    passed: false,
    score: 0,
    evidence: `Grader script threw: ${err instanceof Error ? err.message : String(err)}`,
    details: [],
  };
  process.stdout.write(JSON.stringify(result));
}
