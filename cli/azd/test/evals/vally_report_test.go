// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// various fields from vally's trajectory
const (
	toolName      = "toolName"
	toolArguments = "arguments"
	toolCall      = "tool_call"
)

func TestExtractToolCallsIncludesRelevantArgumentDetails(t *testing.T) {
	events := []event{
		{Type: toolCall, Data: map[string]any{
			toolName:      "run_in_terminal",
			toolArguments: map[string]any{"command": "azd up"},
		}},
		{Type: toolCall, Data: map[string]any{
			toolName:      "skill",
			toolArguments: map[string]any{"skill": "azd"},
		}},
		{Type: toolCall, Data: map[string]any{
			toolName:      "create_file",
			toolArguments: map[string]any{"description": "Create a project file"},
		}},
	}

	got := extractToolCalls(events)
	want := []string{
		"Summary: create_file x1, run_in_terminal x1, skill x1",
		"run_in_terminal: azd up",
		"skill: azd",
		"create_file: Create a project file",
	}
	require.Equal(t, want, got)
}

func TestFilteredEntriesHandlesVallyTrialOutcomes(t *testing.T) {
	records, err := loadRecords(filepath.Join("testdata", "results.jsonl"))
	require.NoError(t, err)

	passed, failed := filteredEntries(records)
	require.Len(t, passed, 1)
	require.Len(t, failed, 3)
	require.Equal(t, "successful-trial", passed[0].stimulus)
	require.Equal(t, statusSuccess, passed[0].status)
	require.True(t, passed[0].passed)

	failedByStimulus := map[string]reportEntry{}
	for _, entry := range failed {
		failedByStimulus[entry.stimulus] = entry
	}

	for _, test := range []struct {
		stimulus   string
		status     string
		diagnostic string
	}{
		{stimulus: "failed-grade", status: statusSuccess},
		{stimulus: "executor-error", status: "error", diagnostic: "The test executor could not start."},
		{stimulus: "skipped-trial", status: "skipped", diagnostic: "The executor is unsupported."},
	} {
		require.Contains(t, failedByStimulus, test.stimulus)
		entry := failedByStimulus[test.stimulus]
		require.Equal(t, test.status, entry.status)
		require.Equal(t, test.diagnostic, entry.diagnostic)
	}
}

func TestBuildReportIncludesDiagnostics(t *testing.T) {
	records, err := loadRecords(filepath.Join("testdata", "results.jsonl"))
	require.NoError(t, err)

	_, failed := filteredEntries(records)
	report := buildReport(failed, 4, "testdata/results.jsonl", "testdata", ".", false)

	for _, want := range []string{
		"- Status: error",
		"- Diagnostic: The test executor could not start.",
		"- Status: skipped",
		"- Diagnostic: The executor is unsupported.",
	} {
		require.Contains(t, report, want)
	}
}

func TestBuildReportDefaultsMissingSingleRunTrialMetadata(t *testing.T) {
	records, err := loadRecords(filepath.Join("testdata", "single-run-results.jsonl"))
	require.NoError(t, err)

	passed, failed := filteredEntries(records)
	require.Len(t, passed, 1)
	require.Empty(t, failed)

	report := buildReport(passed, 1, "testdata/single-run-results.jsonl", "testdata", ".", true)
	require.Contains(t, report, "single-run-trial (trial 1/1, test-model)")
	require.NotContains(t, report, "trial 1/0")
}

func TestExperimentResultsIncludeVariantInReport(t *testing.T) {
	runDir := filepath.Join("testdata", "experiment-run")
	resultsFiles, err := collectResultsFiles(runDir)
	require.NoError(t, err)
	require.Len(t, resultsFiles, 1)

	records, err := loadResults(resultsFiles)
	require.NoError(t, err)
	passed, failed := filteredEntries(records)
	require.Empty(t, failed)
	require.Len(t, passed, 1)

	const variant = "skills=with-skills,model=test-model"
	require.Equal(t, variant, passed[0].experimentVariant)

	renderedReport := buildReport(passed, 1, runDir, runDir, ".", true)
	require.Contains(t, renderedReport, ", "+variant+")")

	prefix := filepath.Join(t.TempDir(), "experiment-results")
	message, err := report(runDir, prefix)
	require.NoError(t, err)
	require.Contains(t, message, "0 failed, 1 passed")

	passedReport, err := os.ReadFile(prefix + "-passed.md")
	require.NoError(t, err)
	require.Contains(t, string(passedReport), ", "+variant+")")

	failedReport, err := os.ReadFile(prefix + "-failed.md")
	require.NoError(t, err)
	require.Contains(t, string(failedReport), "No failed trials in this run.")
}

func TestLatestExperimentRunSelectsNewestValidRun(t *testing.T) {
	baseDir := t.TempDir()
	for _, runName := range []string{
		"2026-07-23T01-00-00-000Z",
		"2026-07-24T01-00-00-000Z",
	} {
		resultsPath := filepath.Join(baseDir, runName, "skills=with-skills", "results.jsonl")
		require.NoError(t, os.MkdirAll(filepath.Dir(resultsPath), 0o755))
		require.NoError(t, os.WriteFile(resultsPath, []byte("{}\n"), 0o600))
	}

	// A newer directory without a variant results file must not be selected.
	require.NoError(t, os.Mkdir(filepath.Join(baseDir, "2026-07-25T01-00-00-000Z"), 0o755))

	got, err := latestExperimentRun(baseDir)
	require.NoError(t, err)
	want := filepath.Join(baseDir, "2026-07-24T01-00-00-000Z")
	require.Equal(t, want, got)
}

func TestLatestRunFromVallyResultsSelectsNewestValidRun(t *testing.T) {
	baseDir := t.TempDir()
	for _, runName := range []string{
		"2026-07-23T01-00-00-000Z",
		"2026-07-24T01-00-00-000Z",
	} {
		resultsPath := filepath.Join(baseDir, runName, "results.jsonl")
		require.NoError(t, os.MkdirAll(filepath.Dir(resultsPath), 0o755))
		require.NoError(t, os.WriteFile(resultsPath, []byte("{}\n"), 0o600))
	}

	// A newer directory without results must not be selected.
	require.NoError(t, os.Mkdir(filepath.Join(baseDir, "2026-07-25T01-00-00-000Z"), 0o755))

	got, err := latestRunFromVallyResults(baseDir)
	require.NoError(t, err)
	want := filepath.Join(baseDir, "2026-07-24T01-00-00-000Z")
	require.Equal(t, want, got)
}

func TestCollectResultsFilesPrefersRootResultsFile(t *testing.T) {
	runDir := t.TempDir()
	rootResultsPath := filepath.Join(runDir, "results.jsonl")
	shardResultsPath := filepath.Join(runDir, "skills=with-skills", "results.jsonl")

	require.NoError(t, os.WriteFile(rootResultsPath, []byte("{}\n"), 0o600))
	require.NoError(t, os.MkdirAll(filepath.Dir(shardResultsPath), 0o755))
	require.NoError(t, os.WriteFile(shardResultsPath, []byte("{}\n"), 0o600))

	got, err := collectResultsFiles(runDir)
	require.NoError(t, err)
	require.Equal(t, []string{rootResultsPath}, got)
}

func TestReportWritesPassedAndFailedReports(t *testing.T) {
	runDir := t.TempDir()
	results, err := os.ReadFile(filepath.Join("testdata", "results.jsonl"))
	require.NoError(t, err)
	// #nosec G703 -- runDir is t.TempDir(); test-only file write, not user input.
	require.NoError(t, os.WriteFile(filepath.Join(runDir, "results.jsonl"), results, 0o600))

	prefix := filepath.Join(t.TempDir(), "eval-results")
	message, err := report(runDir, prefix)
	require.NoError(t, err)
	require.Contains(t, message, "3 failed, 1 passed")

	const (
		reportFailedSuffix = "-failed.md"
		reportPassedSuffix = "-passed.md"
	)

	for _, suffix := range []string{reportFailedSuffix, reportPassedSuffix} {
		path := prefix + suffix
		contents, err := os.ReadFile(path)
		require.NoError(t, err)
		require.NotEmpty(t, contents)
		require.Contains(t, string(contents), "Generated by vally_report.go")
	}

	failedReport, err := os.ReadFile(prefix + reportFailedSuffix)
	require.NoError(t, err)
	require.Contains(t, string(failedReport), "- Failed trials: 3/4")

	passedReport, err := os.ReadFile(prefix + reportPassedSuffix)
	require.NoError(t, err)
	require.Contains(t, string(passedReport), "- Successful trials: 1/4")
}

func TestReportCreatesMissingOutputDirectory(t *testing.T) {
	runDir := t.TempDir()
	results, err := os.ReadFile(filepath.Join("testdata", "results.jsonl"))
	require.NoError(t, err)
	// #nosec G703 -- runDir is t.TempDir(); test-only file write, not user input.
	require.NoError(t, os.WriteFile(filepath.Join(runDir, "results.jsonl"), results, 0o600))

	prefix := filepath.Join(t.TempDir(), "reports", "eval-results")
	_, err = report(runDir, prefix)
	require.NoError(t, err)

	for _, suffix := range []string{"-failed.md", "-passed.md"} {
		require.FileExists(t, prefix+suffix)
	}
}

func TestReportLinksAreRelativeToGeneratedReport(t *testing.T) {
	runDir := t.TempDir()
	results, err := os.ReadFile(filepath.Join("testdata", "results.jsonl"))
	require.NoError(t, err)
	// #nosec G703 -- runDir is t.TempDir(); test-only file write, not user input.
	require.NoError(t, os.WriteFile(filepath.Join(runDir, "results.jsonl"), results, 0o600))

	prefix := filepath.Join(runDir, "vally_report", "eval-results")
	_, err = report(runDir, prefix)
	require.NoError(t, err)

	contents, err := os.ReadFile(prefix + "-failed.md")
	require.NoError(t, err)
	require.Contains(t, string(contents), "](../results.jsonl#L2)")
}

func TestReportLatestRunsReportsBothAvailableRuns(t *testing.T) {
	baseDir := t.TempDir()
	evalResultsDir := filepath.Join(baseDir, "vally-results")
	experimentResultsDir := filepath.Join(baseDir, "vally-experiment-results")

	for _, resultsPath := range []string{
		filepath.Join(evalResultsDir, "2026-07-24T01-00-00-000Z", "results.jsonl"),
		filepath.Join(
			experimentResultsDir,
			"2026-07-24T01-00-00-000Z",
			"skills=with-skills",
			"results.jsonl",
		),
	} {
		require.NoError(t, os.MkdirAll(filepath.Dir(resultsPath), 0o755))
		require.NoError(t, os.WriteFile(resultsPath, []byte("{}\n"), 0o600))
	}

	messages, err := reportLatestRuns(
		evalResultsDir,
		experimentResultsDir,
		baseDir,
		"eval-results",
		"eval-experiments",
	)
	require.NoError(t, err)
	require.Len(t, messages, 2)

	for _, prefix := range []string{"eval-results", "eval-experiments"} {
		for _, suffix := range []string{"-failed.md", "-passed.md"} {
			require.FileExists(t, filepath.Join(baseDir, prefix)+suffix)
		}
	}
}

func TestReportLatestRunsReportsAvailableRunsAndSkipsMissingRuns(t *testing.T) {
	baseDir := t.TempDir()
	evalResultsDir := filepath.Join(baseDir, "vally-results")
	experimentResultsDir := filepath.Join(baseDir, "vally-experiment-results")

	evalResultsPath := filepath.Join(evalResultsDir, "2026-07-24T01-00-00-000Z", "results.jsonl")
	require.NoError(t, os.MkdirAll(filepath.Dir(evalResultsPath), 0o755))
	require.NoError(t, os.WriteFile(evalResultsPath, []byte("{}\n"), 0o600))

	messages, err := reportLatestRuns(
		evalResultsDir,
		experimentResultsDir,
		baseDir,
		"eval-results",
		"eval-experiments",
	)
	require.NoError(t, err)
	require.Len(t, messages, 2)
	require.Contains(t, messages[0], "0 failed, 0 passed")
	require.Contains(t, messages[1], "Skipping "+experimentResultsDir)
}
