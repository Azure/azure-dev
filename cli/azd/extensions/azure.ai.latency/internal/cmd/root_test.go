// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"azure.ai.latency/internal/model"
)

func TestNewRootCommandIncludesExpectedCommands(t *testing.T) {
	root := NewRootCommand()
	for _, name := range []string{"assess", "demo", "version", "metadata"} {
		command, _, err := root.Find([]string{name})
		if err != nil || command.Name() != name {
			t.Fatalf("expected command %q to be registered", name)
		}
	}
	for _, name := range []string{"context", "prompt", "listen", "generate-traffic"} {
		if command, _, err := root.Find([]string{name}); err == nil && command.Name() == name {
			t.Fatalf("did not expect command %q", name)
		}
	}
}

func TestAssessCommandFlags(t *testing.T) {
	root := NewRootCommand()
	command, _, err := root.Find([]string{"assess"})
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{
		"deployment-id",
		"subscription",
		"resource-group",
		"account-name",
		"deployment-name",
		"latency-goal",
		"last",
		"start-time",
		"end-time",
		"report",
		"open",
		"no-progress",
	} {
		if command.Flags().Lookup(name) == nil {
			t.Fatalf("expected --%s", name)
		}
	}
	if got := command.Flags().Lookup("last").DefValue; got != "24h" {
		t.Fatalf("--last default = %q, want 24h", got)
	}
	if got := command.Flags().Lookup("report").DefValue; got != defaultAssessReport {
		t.Fatalf("--report default = %q, want %q", got, defaultAssessReport)
	}
}

func TestDemoListScenarios(t *testing.T) {
	root := NewRootCommand()
	var output bytes.Buffer
	root.SetOut(&output)
	root.SetErr(&output)
	root.SetArgs([]string{"demo", "--list-scenarios"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{
		"within-target: Within current offer target",
		"unexplained-gap: Above target, gap remains unexplained",
		"logs-unavailable: Log Analytics is not enabled",
	} {
		if !strings.Contains(output.String(), expected) {
			t.Fatalf("scenario list does not contain %q:\n%s", expected, output.String())
		}
	}
}

func TestDemoJSONOutput(t *testing.T) {
	root := NewRootCommand()
	var stdout, stderr bytes.Buffer
	root.SetOut(&stdout)
	root.SetErr(&stderr)
	root.SetArgs([]string{
		"demo",
		"--scenario", "workload-explained",
		"--output", "json",
		"--report", "",
	})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	var result model.AssessmentResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("parse JSON output: %v\n%s", err, stdout.String())
	}
	if result.SchemaVersion != model.SchemaVersion ||
		result.Scenario == nil ||
		*result.Scenario != "workload-explained" ||
		result.BenchmarkComparison.Status != model.BenchmarkMatched {
		t.Fatalf("unexpected result: %+v", result)
	}
	if stderr.Len() != 0 {
		t.Fatalf("unexpected stderr: %s", stderr.String())
	}
}

func TestDemoWritesHTMLReport(t *testing.T) {
	reportPath := filepath.Join(t.TempDir(), "nested", "demo.html")
	root := NewRootCommand()
	var stdout, stderr bytes.Buffer
	root.SetOut(&stdout)
	root.SetErr(&stderr)
	root.SetArgs([]string{
		"demo",
		"--scenario", "within-target",
		"--report", reportPath,
	})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(reportPath) //nolint:gosec // The path is created by t.TempDir.
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(content, []byte("Model Latency Self-Service Tool")) {
		t.Fatal("report does not contain title")
	}
	if !strings.Contains(stderr.String(), "HTML report: "+reportPath) {
		t.Fatalf("stderr does not contain report path: %s", stderr.String())
	}
}

func TestWriteAndOpenReportUsesFileURL(t *testing.T) {
	reportPath := filepath.Join(t.TempDir(), "report with spaces.html")
	originalOpen := openReportURL
	var openedURL string
	openReportURL = func(_ context.Context, value string) error {
		openedURL = value
		return nil
	}
	t.Cleanup(func() {
		openReportURL = originalOpen
	})

	result := &model.AssessmentResult{
		SchemaVersion: model.SchemaVersion,
		TrafficProfile: model.TrafficProfile{
			Latency: map[string]model.LatencyDistribution{},
		},
	}
	command := NewRootCommand()
	command.SetErr(&bytes.Buffer{})
	if err := writeAndOpenReport(command, reportPath, true, []*model.AssessmentResult{result}); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(openedURL, "file://") || strings.Contains(openedURL, `\`) {
		t.Fatalf("opened URL = %q, want an absolute file URL", openedURL)
	}
	if !strings.Contains(openedURL, "report%20with%20spaces.html") {
		t.Fatalf("opened URL does not escape spaces: %q", openedURL)
	}
	if runtime.GOOS == "windows" && !strings.HasPrefix(openedURL, "file:///") {
		t.Fatalf("Windows file URL = %q, want file:/// prefix", openedURL)
	}
}

func TestNormalizeExplorerOpenError(t *testing.T) {
	if err := normalizeExplorerOpenError(t.Context(), &exec.ExitError{}); err != nil {
		t.Fatalf("Explorer handoff exit error = %v, want nil", err)
	}

	expected := errors.New("could not start Explorer")
	if err := normalizeExplorerOpenError(t.Context(), expected); !errors.Is(err, expected) {
		t.Fatalf("start error = %v, want %v", err, expected)
	}

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := normalizeExplorerOpenError(ctx, &exec.ExitError{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled error = %v, want context canceled", err)
	}
}

func TestAssessNoPromptReportsMissingFieldsBeforeConnecting(t *testing.T) {
	root := NewRootCommand()
	var output bytes.Buffer
	root.SetOut(&output)
	root.SetErr(&output)
	root.SetArgs([]string{"assess", "--no-prompt", "--no-progress", "--report", ""})
	err := root.Execute()
	if err == nil {
		t.Fatal("expected missing deployment reference to fail")
	}
	for _, expected := range []string{"--subscription", "--deployment-name"} {
		if !strings.Contains(err.Error(), expected) {
			t.Fatalf("error %q does not contain %q", err, expected)
		}
	}
}

func TestVersionUsesInjectedWriter(t *testing.T) {
	root := NewRootCommand()
	var output bytes.Buffer
	root.SetOut(&output)
	root.SetErr(&output)
	root.SetArgs([]string{"version"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	if got := output.String(); got != "azure.ai.latency "+Version+"\n" {
		t.Fatalf("version output = %q", got)
	}
}
