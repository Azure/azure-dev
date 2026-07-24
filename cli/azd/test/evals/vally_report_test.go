// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExtractToolCallsIncludesRelevantArgumentDetails(t *testing.T) {
	events := []event{
		{Type: "tool_call", Data: map[string]any{
			"toolName":  "run_in_terminal",
			"arguments": map[string]any{"command": "azd up"},
		}},
		{Type: "tool_call", Data: map[string]any{
			"toolName":  "skill",
			"arguments": map[string]any{"skill": "azd"},
		}},
		{Type: "tool_call", Data: map[string]any{
			"toolName":  "create_file",
			"arguments": map[string]any{"description": "Create a project file"},
		}},
	}

	got := extractToolCalls(events)
	want := []string{
		"Summary: create_file x1, run_in_terminal x1, skill x1",
		"run_in_terminal: azd up",
		"skill: azd",
		"create_file: Create a project file",
	}
	if len(got) != len(want) {
		t.Fatalf("extractToolCalls() = %v, want %v", got, want)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Errorf("extractToolCalls()[%d] = %q, want %q", index, got[index], want[index])
		}
	}
}

func TestFilteredEntriesHandlesVallyTrialOutcomes(t *testing.T) {
	records, err := loadRecords(filepath.Join("testdata", "results.jsonl"))
	if err != nil {
		t.Fatalf("loadRecords() error = %v", err)
	}

	passed, failed := filteredEntries(records)
	if len(passed) != 1 {
		t.Fatalf("passed entries = %d, want 1", len(passed))
	}
	if len(failed) != 3 {
		t.Fatalf("failed entries = %d, want 3", len(failed))
	}

	if got := passed[0]; got.stimulus != "successful-trial" || got.status != "success" || !got.passed {
		t.Errorf("passed entry = %+v, want successful trial", got)
	}

	failedByStimulus := map[string]reportEntry{}
	for _, entry := range failed {
		failedByStimulus[entry.stimulus] = entry
	}

	for _, test := range []struct {
		stimulus   string
		status     string
		diagnostic string
	}{
		{stimulus: "failed-grade", status: "success"},
		{stimulus: "executor-error", status: "error", diagnostic: "The test executor could not start."},
		{stimulus: "skipped-trial", status: "skipped", diagnostic: "The executor is unsupported."},
	} {
		entry, ok := failedByStimulus[test.stimulus]
		if !ok {
			t.Errorf("missing failed entry for %q", test.stimulus)
			continue
		}
		if entry.status != test.status || entry.diagnostic != test.diagnostic {
			t.Errorf("entry %q = status %q, diagnostic %q; want status %q, diagnostic %q",
				test.stimulus, entry.status, entry.diagnostic, test.status, test.diagnostic)
		}
	}
}

func TestBuildReportIncludesDiagnostics(t *testing.T) {
	records, err := loadRecords(filepath.Join("testdata", "results.jsonl"))
	if err != nil {
		t.Fatalf("loadRecords() error = %v", err)
	}

	_, failed := filteredEntries(records)
	report := buildReport(failed, 4, "testdata/results.jsonl", "testdata", false)

	for _, want := range []string{
		"- Status: error",
		"- Diagnostic: The test executor could not start.",
		"- Status: skipped",
		"- Diagnostic: The executor is unsupported.",
	} {
		if !strings.Contains(report, want) {
			t.Errorf("report does not contain %q", want)
		}
	}
}

func TestExperimentResultsIncludeVariantInReport(t *testing.T) {
	runDir := filepath.Join("testdata", "experiment-run")
	resultsFiles, err := collectResultsFiles(runDir)
	if err != nil {
		t.Fatalf("collectResultsFiles() error = %v", err)
	}
	if len(resultsFiles) != 1 {
		t.Fatalf("result files = %d, want 1", len(resultsFiles))
	}

	records, err := loadResults(resultsFiles)
	if err != nil {
		t.Fatalf("loadResults() error = %v", err)
	}
	passed, failed := filteredEntries(records)
	if len(failed) != 0 {
		t.Fatalf("failed entries = %d, want 0", len(failed))
	}
	if len(passed) != 1 {
		t.Fatalf("passed entries = %d, want 1", len(passed))
	}

	const variant = "skills=with-skills,model=test-model"
	if passed[0].experimentVariant != variant {
		t.Errorf("experiment variant = %q, want %q", passed[0].experimentVariant, variant)
	}

	renderedReport := buildReport(passed, 1, runDir, runDir, true)
	if !strings.Contains(renderedReport, ", "+variant+")") {
		t.Errorf("report does not contain experiment variant %q", variant)
	}

	prefix := filepath.Join(t.TempDir(), "experiment-results")
	message, err := report(runDir, prefix)
	if err != nil {
		t.Fatalf("report() error = %v", err)
	}
	if !strings.Contains(message, "0 failed, 1 passed") {
		t.Errorf("report() message = %q, want counts", message)
	}

	passedReport, err := os.ReadFile(prefix + "-passed.md")
	if err != nil {
		t.Fatalf("ReadFile(passed report) error = %v", err)
	}
	if !strings.Contains(string(passedReport), ", "+variant+")") {
		t.Errorf("passed report does not contain experiment variant %q", variant)
	}

	failedReport, err := os.ReadFile(prefix + "-failed.md")
	if err != nil {
		t.Fatalf("ReadFile(failed report) error = %v", err)
	}
	if !strings.Contains(string(failedReport), "No failed trials in this run.") {
		t.Error("failed report does not contain the expected empty state")
	}
}

func TestLatestExperimentRunSelectsNewestValidRun(t *testing.T) {
	baseDir := t.TempDir()
	for _, runName := range []string{
		"2026-07-23T01-00-00-000Z",
		"2026-07-24T01-00-00-000Z",
	} {
		resultsPath := filepath.Join(baseDir, runName, "skills=with-skills", "results.jsonl")
		if err := os.MkdirAll(filepath.Dir(resultsPath), 0o755); err != nil {
			t.Fatalf("MkdirAll(%q) error = %v", resultsPath, err)
		}
		if err := os.WriteFile(resultsPath, []byte("{}\n"), 0o600); err != nil {
			t.Fatalf("WriteFile(%q) error = %v", resultsPath, err)
		}
	}

	// A newer directory without a variant results file must not be selected.
	if err := os.Mkdir(filepath.Join(baseDir, "2026-07-25T01-00-00-000Z"), 0o755); err != nil {
		t.Fatalf("Mkdir() error = %v", err)
	}

	got, err := latestExperimentRun(baseDir)
	if err != nil {
		t.Fatalf("latestExperimentRun() error = %v", err)
	}
	want := filepath.Join(baseDir, "2026-07-24T01-00-00-000Z")
	if got != want {
		t.Errorf("latestExperimentRun() = %q, want %q", got, want)
	}
}

func TestLatestRunFromVallyResultsSelectsNewestValidRun(t *testing.T) {
	baseDir := t.TempDir()
	for _, runName := range []string{
		"2026-07-23T01-00-00-000Z",
		"2026-07-24T01-00-00-000Z",
	} {
		resultsPath := filepath.Join(baseDir, runName, "results.jsonl")
		if err := os.MkdirAll(filepath.Dir(resultsPath), 0o755); err != nil {
			t.Fatalf("MkdirAll(%q) error = %v", resultsPath, err)
		}
		if err := os.WriteFile(resultsPath, []byte("{}\n"), 0o600); err != nil {
			t.Fatalf("WriteFile(%q) error = %v", resultsPath, err)
		}
	}

	// A newer directory without results must not be selected.
	if err := os.Mkdir(filepath.Join(baseDir, "2026-07-25T01-00-00-000Z"), 0o755); err != nil {
		t.Fatalf("Mkdir() error = %v", err)
	}

	got, err := latestRunFromVallyResults(baseDir)
	if err != nil {
		t.Fatalf("latestRunFromVallyResults() error = %v", err)
	}
	want := filepath.Join(baseDir, "2026-07-24T01-00-00-000Z")
	if got != want {
		t.Errorf("latestRunFromVallyResults() = %q, want %q", got, want)
	}
}

func TestCollectResultsFilesPrefersRootResultsFile(t *testing.T) {
	runDir := t.TempDir()
	rootResultsPath := filepath.Join(runDir, "results.jsonl")
	shardResultsPath := filepath.Join(runDir, "skills=with-skills", "results.jsonl")

	if err := os.WriteFile(rootResultsPath, []byte("{}\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(%q) error = %v", rootResultsPath, err)
	}
	if err := os.MkdirAll(filepath.Dir(shardResultsPath), 0o755); err != nil {
		t.Fatalf("MkdirAll(%q) error = %v", shardResultsPath, err)
	}
	if err := os.WriteFile(shardResultsPath, []byte("{}\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(%q) error = %v", shardResultsPath, err)
	}

	got, err := collectResultsFiles(runDir)
	if err != nil {
		t.Fatalf("collectResultsFiles() error = %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("collectResultsFiles() returned %d files, want 1", len(got))
	}
	if got[0] != rootResultsPath {
		t.Errorf("collectResultsFiles() = %q, want root result %q", got[0], rootResultsPath)
	}
}

func TestReportWritesPassedAndFailedReports(t *testing.T) {
	runDir := t.TempDir()
	results, err := os.ReadFile(filepath.Join("testdata", "results.jsonl"))
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(runDir, "results.jsonl"), results, 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	prefix := filepath.Join(t.TempDir(), "eval-results")
	message, err := report(runDir, prefix)
	if err != nil {
		t.Fatalf("report() error = %v", err)
	}
	if !strings.Contains(message, "3 failed, 1 passed") {
		t.Errorf("report() message = %q, want counts", message)
	}

	for _, suffix := range []string{"-failed.md", "-passed.md"} {
		path := prefix + suffix
		contents, err := os.ReadFile(path)
		if err != nil {
			t.Errorf("ReadFile(%q) error = %v", path, err)
			continue
		}
		if len(contents) == 0 {
			t.Errorf("report %q is empty", path)
		}
		if !strings.Contains(string(contents), "Generated by vally_report.go") {
			t.Errorf("report %q does not identify its generator", path)
		}
	}

	failedReport, err := os.ReadFile(prefix + "-failed.md")
	if err != nil {
		t.Fatalf("ReadFile(failed report) error = %v", err)
	}
	if !strings.Contains(string(failedReport), "- Failed trials: 3/4") {
		t.Errorf("failed report does not contain the expected trial count")
	}

	passedReport, err := os.ReadFile(prefix + "-passed.md")
	if err != nil {
		t.Fatalf("ReadFile(passed report) error = %v", err)
	}
	if !strings.Contains(string(passedReport), "- Successful trials: 1/4") {
		t.Errorf("passed report does not contain the expected trial count")
	}
}

func TestReportCreatesMissingOutputDirectory(t *testing.T) {
	runDir := t.TempDir()
	results, err := os.ReadFile(filepath.Join("testdata", "results.jsonl"))
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(runDir, "results.jsonl"), results, 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	prefix := filepath.Join(t.TempDir(), "reports", "eval-results")
	if _, err := report(runDir, prefix); err != nil {
		t.Fatalf("report() error = %v", err)
	}

	for _, suffix := range []string{"-failed.md", "-passed.md"} {
		if _, err := os.Stat(prefix + suffix); err != nil {
			t.Errorf("Stat(%q) error = %v", prefix+suffix, err)
		}
	}
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
		if err := os.MkdirAll(filepath.Dir(resultsPath), 0o755); err != nil {
			t.Fatalf("MkdirAll(%q) error = %v", resultsPath, err)
		}
		if err := os.WriteFile(resultsPath, []byte("{}\n"), 0o600); err != nil {
			t.Fatalf("WriteFile(%q) error = %v", resultsPath, err)
		}
	}

	messages, err := reportLatestRuns(
		evalResultsDir,
		experimentResultsDir,
		baseDir,
		"eval-results",
		"eval-experiments",
	)
	if err != nil {
		t.Fatalf("reportLatestRuns() error = %v", err)
	}
	if len(messages) != 2 {
		t.Fatalf("messages = %d, want 2", len(messages))
	}

	for _, prefix := range []string{"eval-results", "eval-experiments"} {
		for _, suffix := range []string{"-failed.md", "-passed.md"} {
			if _, err := os.Stat(filepath.Join(baseDir, prefix) + suffix); err != nil {
				t.Errorf("Stat(%q) error = %v", prefix+suffix, err)
			}
		}
	}
}

func TestReportLatestRunsReportsAvailableRunsAndSkipsMissingRuns(t *testing.T) {
	baseDir := t.TempDir()
	evalResultsDir := filepath.Join(baseDir, "vally-results")
	experimentResultsDir := filepath.Join(baseDir, "vally-experiment-results")

	evalResultsPath := filepath.Join(evalResultsDir, "2026-07-24T01-00-00-000Z", "results.jsonl")
	if err := os.MkdirAll(filepath.Dir(evalResultsPath), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(evalResultsPath, []byte("{}\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	messages, err := reportLatestRuns(
		evalResultsDir,
		experimentResultsDir,
		baseDir,
		"eval-results",
		"eval-experiments",
	)
	if len(messages) != 1 {
		t.Fatalf("messages = %d, want 1", len(messages))
	}
	if !strings.Contains(messages[0], "0 failed, 0 passed") {
		t.Errorf("message = %q, want empty run counts", messages[0])
	}
	if err == nil || !strings.Contains(err.Error(), "skipping "+experimentResultsDir) {
		t.Errorf("error = %v, want skipped experiment results", err)
	}
}
