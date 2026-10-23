// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package analysis

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"azure.ai.content-understanding/internal/cuclient"
	"azure.ai.content-understanding/internal/testutil"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const terminal = `{"status":"Succeeded","result":{"contents":[{"kind":"document","mimeType":"application/pdf",` +
	`"startPageNumber":2,"endPageNumber":2,"markdown":"Invoice","fields":{"Count":{"type":"integer",` +
	`"valueInteger":9007199254740993}}}],"warnings":[{"code":"Warning","message":"Review this result."}]},` +
	`"usage":{"documentPagesStandard":0,"tokens":{"model":4},"future":9007199254740993},"future":true}`

func writeInput(t *testing.T, directory, name string) string {
	t.Helper()
	path := filepath.Join(directory, name)
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
	require.NoError(t, os.WriteFile(path, []byte(name), 0o600))
	return path
}

func discoverFiles(t *testing.T, names ...string) []Input {
	t.Helper()
	directory := testutil.TempDir(t)
	var paths []string
	for _, name := range names {
		paths = append(paths, writeInput(t, directory, name))
	}
	inputs, err := Discover(t.Context(), InputOptions{Files: paths})
	require.NoError(t, err)
	return inputs
}

func requireCode(t *testing.T, err error, code string) {
	t.Helper()
	domain, ok := errors.AsType[*cuclient.Error](err)
	require.True(t, ok, "expected domain error, got %v", err)
	assert.Equal(t, code, domain.Code)
}

func TestDiscoveryDeterministicPatternsAndRecursion(t *testing.T) {
	directory := testutil.TempDir(t)
	for _, name := range []string{
		"a/nested.pdf", "a.pdf", "b.pdf", "c.txt", ".hidden.pdf", ".hidden-dir/x.pdf",
		"a/result.result.json", "output.result.md", "a/.secret.pdf",
	} {
		writeInput(t, directory, name)
	}
	inputs, err := Discover(t.Context(), InputOptions{Sources: []string{directory}, Recursive: true, Pattern: new("*.pdf")})
	require.NoError(t, err)
	var names []string
	for _, input := range inputs {
		names = append(names, filepath.ToSlash(input.Relative))
		assert.True(t, filepath.IsAbs(input.Path))
	}
	assert.Equal(t, []string{"a.pdf", "a/nested.pdf", "b.pdf"}, names)
	inputs, err = Discover(t.Context(), InputOptions{Sources: []string{directory}})
	require.NoError(t, err)
	require.Len(t, inputs, 3)
	for _, input := range inputs {
		assert.NotContains(t, input.Relative, string(filepath.Separator))
	}
	_, err = Discover(t.Context(), InputOptions{Sources: []string{directory}, Pattern: new("*.missing")})
	requireCode(t, err, "empty_inputs")
}

func TestDiscoveryInputModesAndValidation(t *testing.T) {
	directory := testutil.TempDir(t)
	file := writeInput(t, directory, "a.pdf")
	for _, test := range []struct {
		name string
		opts InputOptions
		code string
	}{
		{"none", InputOptions{}, "invalid_input_mode"},
		{"mixed", InputOptions{Positional: []string{file}, Files: []string{file}}, "invalid_input_mode"},
		{"named mixed", InputOptions{Files: []string{file}, URLs: []string{"https://example.com/a"}}, "invalid_input_mode"},
		{"duplicate", InputOptions{Files: []string{file, file}}, "duplicate_input"},
		{"pattern without source", InputOptions{Files: []string{file}, Pattern: new("*")}, "invalid_pattern"},
		{"empty pattern", InputOptions{Sources: []string{directory}, Pattern: new("")}, "invalid_pattern"},
		{"recursive file", InputOptions{Files: []string{file}, Recursive: true}, "invalid_recursion"},
		{"file directory", InputOptions{Files: []string{directory}}, "invalid_input_file"},
		{"source file", InputOptions{Sources: []string{file}}, "invalid_source"},
		{"missing", InputOptions{Files: []string{file + ".missing"}}, "invalid_input_path"},
		{"wildcard", InputOptions{Positional: []string{directory + "/*.pdf"}}, "input_wildcard"},
		{"url file", InputOptions{Files: []string{"https://example.com/a"}}, "invalid_input_mode"},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := Discover(t.Context(), test.opts)
			requireCode(t, err, test.code)
		})
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err := Discover(ctx, InputOptions{Files: []string{file}})
	require.ErrorIs(t, err, context.Canceled)
}

func TestDiscoveryDeduplicatesPhysicalOverlapAndDoesNotFollowDirectoryLinks(t *testing.T) {
	directory := testutil.TempDir(t)
	file := writeInput(t, directory, "file.pdf")
	require.NoError(t, os.Link(file, filepath.Join(directory, "hardlink.pdf")))
	inputs, err := Discover(t.Context(), InputOptions{Positional: []string{file, directory}})
	require.NoError(t, err)
	require.Len(t, inputs, 1)
	outside := testutil.TempDir(t)
	writeInput(t, outside, "outside.pdf")
	if err := os.Symlink(outside, filepath.Join(directory, "linked-directory")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	require.NoError(t, os.Symlink(file, filepath.Join(directory, "link.pdf")))
	inputs, err = Discover(t.Context(), InputOptions{Sources: []string{directory}, Recursive: true})
	require.NoError(t, err)
	require.Len(t, inputs, 1)
}

func TestRemoteInputValidationRedactionAndNames(t *testing.T) {
	raw := "https://example.com/a%20b.pdf?sig=private&x=2#private"
	inputs, err := Discover(t.Context(), InputOptions{URLs: []string{raw}})
	require.NoError(t, err)
	assert.Equal(t, raw, inputs[0].URL)
	assert.Equal(t, "https://example.com/a%20b.pdf?REDACTED", inputs[0].Reference)
	assert.Equal(t, "a%20b.pdf", inputs[0].Relative)
	for _, invalid := range []string{
		"http://example.com/a", "https://user:secret@example.com/a", "https:///a", "https://example.com:65536/a",
		"https://example.com/a b", "https://example.com/%gg", "https://example.com/" + strings.Repeat("a", 8192),
		"file:///secret", "https://example.com:\n80/a",
	} {
		_, err := Discover(t.Context(), InputOptions{URLs: []string{invalid}})
		requireCode(t, err, "invalid_input_url")
		assert.NotContains(t, err.Error(), "secret")
	}
	for _, test := range []struct{ raw, name string }{
		{"https://example.com/", "remote-input"}, {"https://example.com/NUL.pdf", "_NUL.pdf"},
		{"https://example.com/COM9.txt", "_COM9.txt"}, {"https://example.com/a:b", "a_b"},
		{"https://example.com/甲乙.pdf?sig=private", "甲乙.pdf"},
	} {
		input, err := remoteInput(test.raw)
		require.NoError(t, err)
		assert.Equal(t, test.name, input.Relative)
	}
	limit := "https://example.com/" + strings.Repeat("a", 8192-len("https://example.com/"))
	_, err = remoteInput(limit)
	require.NoError(t, err)
	_, err = remoteInput(limit + "a")
	requireCode(t, err, "invalid_input_url")
}

func TestFnmatchPatterns(t *testing.T) {
	for _, test := range []struct {
		pattern, name string
		match         bool
	}{
		{"*.pdf", "sub/a.pdf", true}, {"a?.pdf", "ab.pdf", true},
		{"[!ab].pdf", "c.pdf", true}, {"[!ab].pdf", "a.pdf", false},
		{"[^a].pdf", "^.pdf", true}, {"[^a].pdf", "b.pdf", false},
		{"[[]x", "[x", true}, {"[]a].pdf", "].pdf", true}, {"[abc", "[abc", true},
		{"*.pdf", "a.txt", false},
	} {
		t.Run(test.pattern+"/"+test.name, func(t *testing.T) {
			match, err := globPattern(test.pattern)
			require.NoError(t, err)
			assert.Equal(t, test.match, match.MatchString(test.name))
		})
	}
}

func TestOutputPlanningPoliciesCollisionsAndRelativePaths(t *testing.T) {
	inputs := discoverFiles(t, "one/a.pdf", "two/a.pdf")
	directory := filepath.Join(testutil.TempDir(t), "not-created")
	plan, err := PlanOutputs(inputs, OutputOptions{Directory: directory, Existing: "error"})
	require.NoError(t, err)
	assert.NotEqual(t, *plan.Outputs[0].Path, *plan.Outputs[1].Path)
	for _, output := range plan.Outputs {
		assert.Regexp(t, `a\.pdf\.[0-9a-f]{8}\.result\.json$`, *output.Path)
	}
	_, err = os.Stat(directory)
	require.ErrorIs(t, err, fs.ErrNotExist)
	again, err := PlanOutputs(inputs, OutputOptions{Directory: directory, Existing: "error"})
	require.NoError(t, err)
	assert.Equal(t, plan, again)
	source := filepath.Dir(filepath.Dir(inputs[0].Path))
	discovered, err := Discover(t.Context(), InputOptions{Sources: []string{source}, Recursive: true})
	require.NoError(t, err)
	plan, err = PlanOutputs(discovered, OutputOptions{Directory: directory, Existing: "error", LLMInput: true})
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(directory, "one", "a.pdf.result.md"), *plan.Outputs[0].Path)
	path := filepath.Join(testutil.TempDir(t), "existing")
	require.NoError(t, os.WriteFile(path, []byte("existing"), 0o600))
	for _, policy := range []string{"error", "skip", "reanalyze"} {
		plan, err := PlanOutputs(inputs[:1], OutputOptions{File: path, Existing: policy})
		if policy == "error" {
			requireCode(t, err, "result_exists")
		} else {
			require.NoError(t, err)
			assert.True(t, plan.Outputs[0].Exists)
			assert.Equal(t, policy == "skip", plan.Outputs[0].Skipped)
		}
	}
}

func TestOutputPlanningRejectsUnsafeAndIncompatiblePaths(t *testing.T) {
	inputs := discoverFiles(t, "a.pdf", "b.pdf")
	directory := testutil.TempDir(t)
	report := writeInput(t, directory, "report.json")
	output := filepath.Join(directory, "output.json")
	for _, test := range []struct {
		name    string
		inputs  []Input
		options OutputOptions
		code    string
	}{
		{"empty", nil, OutputOptions{Existing: "error"}, "empty_inputs"},
		{"policy", inputs, OutputOptions{Existing: "bad"}, "invalid_existing_policy"},
		{"multi file", inputs, OutputOptions{Existing: "error", File: output}, "invalid_output_file"},
		{"file and dir", inputs[:1], OutputOptions{
			Existing: "error", File: output, Directory: directory,
		}, "invalid_output_file"},
		{"overwrite input", inputs[:1], OutputOptions{
			Existing: "reanalyze", File: inputs[0].Path,
		}, "output_overwrites_input"},
		{"existing report", inputs, OutputOptions{
			Existing: "error", Directory: directory, Report: report,
		}, "invalid_report_path"},
		{"report collision", inputs[:1], OutputOptions{
			Existing: "error", File: output, Report: output,
		}, "report_output_collision"},
		{"directory output", inputs[:1], OutputOptions{Existing: "reanalyze", File: directory}, "unsafe_output_path"},
		{"parent collision", inputs[:1], OutputOptions{
			Existing: "error", File: output, Report: filepath.Join(output, "child"),
		}, "output_parent_collision"},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := PlanOutputs(test.inputs, test.options)
			requireCode(t, err, test.code)
		})
	}
	alias := filepath.Join(directory, "hardlink")
	require.NoError(t, os.Link(inputs[0].Path, alias))
	_, err := PlanOutputs(inputs[:1], OutputOptions{Existing: "reanalyze", File: alias})
	requireCode(t, err, "output_overwrites_input")
	remote, err := Discover(t.Context(), InputOptions{URLs: []string{
		"https://example.com/a.pdf?sig=secret", "https://example.org/a.pdf",
	}})
	require.NoError(t, err)
	_, err = PlanOutputs(remote, OutputOptions{Existing: "error"})
	requireCode(t, err, "missing_output_directory")
	_, err = PlanOutputs(remote, OutputOptions{Existing: "error", Directory: directory})
	requireCode(t, err, "remote_output_collision")
	remote[0].Relative = strings.Repeat("a", 229)
	_, err = PlanOutputs(remote[:1], OutputOptions{Existing: "error", Directory: directory})
	requireCode(t, err, "output_name_too_long")
	inputs[0].Relative = "../escape"
	_, err = PlanOutputs(inputs, OutputOptions{Existing: "error", Directory: directory})
	requireCode(t, err, "invalid_relative_output")
}

type fakeAnalysisClient struct {
	cuclient.Client
	analyze func(context.Context, cuclient.AnalyzeRequest) (cuclient.AnalyzeResponse, error)
}

func (f fakeAnalysisClient) Analyze(ctx context.Context, request cuclient.AnalyzeRequest) (cuclient.AnalyzeResponse, error) {
	return f.analyze(ctx, request)
}

func runOptions() RunOptions {
	return RunOptions{Analyzer: "prebuilt-invoice", Mode: cuclient.AnalysisModeAsync, Concurrency: 4, Usage: true}
}

func successfulClient() fakeAnalysisClient {
	return fakeAnalysisClient{analyze: func(context.Context, cuclient.AnalyzeRequest) (cuclient.AnalyzeResponse, error) {
		return cuclient.AnalyzeResponse{JSON: json.RawMessage(terminal)}, nil
	}}
}

func TestExecutionStreamingFileOutputUsageAndReportGolden(t *testing.T) {
	inputs := discoverFiles(t, "a.pdf")
	directory := testutil.TempDir(t)
	streamPlan, err := PlanOutputs(inputs, OutputOptions{Existing: "error"})
	require.NoError(t, err)
	result, err := Execute(t.Context(), successfulClient(), streamPlan, runOptions())
	require.NoError(t, err)
	encoded, err := json.Marshal(result.Stream)
	require.NoError(t, err)
	assert.JSONEq(t, `{"result":`+terminal+`,"usage":`+
		`{"documentPagesStandard":0,"tokens":{"model":4},"future":9007199254740993}}`, string(encoded))
	assert.Contains(t, string(encoded), "9007199254740993")
	reportPath := filepath.Join(directory, "report.json")
	plan, err := PlanOutputs(inputs, OutputOptions{Existing: "error", Directory: directory, Report: reportPath})
	require.NoError(t, err)
	result, err = Execute(t.Context(), successfulClient(), plan, runOptions())
	require.NoError(t, err)
	require.Nil(t, result.Stream)
	// #nosec G304 -- The result path was planned inside this test's temporary directory.
	body, err := os.ReadFile(*plan.Outputs[0].Path)
	require.NoError(t, err)
	assert.JSONEq(t, terminal, string(body))
	assert.Contains(t, string(body), "9007199254740993")
	// #nosec G304 -- Reads only this test's private temporary report.
	body, err = os.ReadFile(reportPath)
	require.NoError(t, err)
	inputJSON, err := json.Marshal(inputs[0].Reference)
	require.NoError(t, err)
	outputJSON, err := json.Marshal(*plan.Outputs[0].Path)
	require.NoError(t, err)
	normalized := strings.ReplaceAll(string(body), string(inputJSON), `"<input>"`)
	normalized = strings.ReplaceAll(normalized, string(outputJSON), `"<output>"`)
	golden, err := os.ReadFile("testdata/report.json")
	require.NoError(t, err)
	assert.JSONEq(t, string(golden), normalized)
	assert.NotContains(t, string(body), "analyzerId")
}

func TestExecutionBoundedConcurrencyBodyLifetimeAndStableOrder(t *testing.T) {
	inputs := discoverFiles(t, "a.pdf", "b.pdf", "c.pdf", "d.pdf", "e.pdf")
	plan, err := PlanOutputs(inputs, OutputOptions{Existing: "error", Directory: testutil.TempDir(t)})
	require.NoError(t, err)
	var running, maximum, calls atomic.Int32
	var mutex sync.Mutex
	var readers []io.ReadSeeker
	arrivals := make(chan struct{}, 2)
	release := make(chan struct{})
	client := fakeAnalysisClient{analyze: func(
		ctx context.Context, request cuclient.AnalyzeRequest,
	) (cuclient.AnalyzeResponse, error) {
		count := running.Add(1)
		defer running.Add(-1)
		for current := maximum.Load(); count > current; current = maximum.Load() {
			if maximum.CompareAndSwap(current, count) {
				break
			}
		}
		call := calls.Add(1)
		require.Equal(t, "application/octet-stream", request.Input.ContentType)
		require.Equal(t, cuclient.AnalysisModeAsync, request.Mode)
		body, err := io.ReadAll(request.Input.Body)
		require.NoError(t, err)
		require.NotEmpty(t, body)
		_, err = request.Input.Body.Seek(0, io.SeekStart)
		require.NoError(t, err)
		mutex.Lock()
		readers = append(readers, request.Input.Body)
		mutex.Unlock()
		if call <= 2 {
			arrivals <- struct{}{}
			select {
			case <-release:
			case <-ctx.Done():
				return cuclient.AnalyzeResponse{}, ctx.Err()
			}
		}
		return cuclient.AnalyzeResponse{JSON: json.RawMessage(terminal)}, nil
	}}
	options := runOptions()
	options.Concurrency = 2
	type executionResult struct {
		execution Execution
		err       error
	}
	done := make(chan executionResult, 1)
	go func() {
		result, err := Execute(t.Context(), client, plan, options)
		done <- executionResult{result, err}
	}()
	for range 2 {
		select {
		case <-arrivals:
		case <-time.After(5 * time.Second):
			t.Fatal("workers did not reach the concurrency limit")
		}
	}
	assert.EqualValues(t, 2, calls.Load())
	close(release)
	finished := <-done
	require.NoError(t, finished.err)
	for index, record := range finished.execution.Records {
		assert.Equal(t, inputs[index].Reference, record.Input)
	}
	assert.EqualValues(t, 2, maximum.Load())
	assert.EqualValues(t, len(inputs), calls.Load())
	for _, reader := range readers {
		_, err := reader.Read(make([]byte, 1))
		require.Error(t, err, "caller-owned binary reader must close after Analyze")
	}
}

func TestExecutionCancellationStopsPendingJobsAndSavesReport(t *testing.T) {
	inputs := discoverFiles(t, "a.pdf", "b.pdf", "c.pdf")
	reportPath := filepath.Join(testutil.TempDir(t), "report.json")
	plan, err := PlanOutputs(inputs, OutputOptions{Existing: "error", Directory: testutil.TempDir(t), Report: reportPath})
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	var calls atomic.Int32
	client := fakeAnalysisClient{analyze: func(
		ctx context.Context, _ cuclient.AnalyzeRequest,
	) (cuclient.AnalyzeResponse, error) {
		calls.Add(1)
		cancel()
		<-ctx.Done()
		return cuclient.AnalyzeResponse{}, ctx.Err()
	}}
	options := runOptions()
	options.Concurrency = 1
	result, err := Execute(ctx, client, plan, options)
	require.ErrorIs(t, err, context.Canceled)
	assert.EqualValues(t, 1, calls.Load())
	require.Len(t, result.Records, 3)
	for _, record := range result.Records {
		assert.Equal(t, "failed", record.Status)
		_, err := os.Stat(*record.Output)
		require.ErrorIs(t, err, fs.ErrNotExist)
	}
	// #nosec G304 -- Reads only this test's private temporary report.
	body, err := os.ReadFile(reportPath)
	require.NoError(t, err)
	assert.Contains(t, string(body), `"failed": 3`)
}

func TestExecutionPartialFailureSkipAndRedaction(t *testing.T) {
	inputs, err := Discover(t.Context(), InputOptions{URLs: []string{
		"https://example.com/good.pdf?sig=secret", "https://example.com/bad.pdf?sig=secret",
		"https://example.com/skip.pdf?sig=secret",
	}})
	require.NoError(t, err)
	directory := testutil.TempDir(t)
	writeInput(t, directory, "skip.pdf.result.json")
	reportPath := filepath.Join(directory, "report.json")
	plan, err := PlanOutputs(inputs, OutputOptions{Existing: "skip", Directory: directory, Report: reportPath})
	require.NoError(t, err)
	var calls atomic.Int32
	client := fakeAnalysisClient{analyze: func(
		_ context.Context, request cuclient.AnalyzeRequest,
	) (cuclient.AnalyzeResponse, error) {
		calls.Add(1)
		assert.Contains(t, request.Input.URL, "?sig=secret")
		if strings.Contains(request.Input.URL, "/bad.pdf") {
			return cuclient.AnalyzeResponse{}, &cuclient.Error{
				Kind: cuclient.ErrorService, Message: "Cannot analyze " + request.Input.URL + "#secret",
			}
		}
		return cuclient.AnalyzeResponse{JSON: json.RawMessage(terminal)}, nil
	}}
	result, err := Execute(t.Context(), client, plan, runOptions())
	requireCode(t, err, "analysis_failed")
	assert.EqualValues(t, 2, calls.Load())
	require.Len(t, result.Records, 3)
	assert.Equal(t, "succeeded", result.Records[0].Status)
	assert.Equal(t, "failed", result.Records[1].Status)
	assert.Equal(t, "skipped", result.Records[2].Status)
	// #nosec G304 -- Reads only this test's private temporary report.
	body, err := os.ReadFile(reportPath)
	require.NoError(t, err)
	assert.NotContains(t, string(body), "secret")
	assert.Contains(t, string(body), "REDACTED")
	assert.Contains(t, string(body), `"succeeded": 1`)
	assert.Contains(t, string(body), `"skipped": 1`)
}

func TestExecutionPublicationFailureStopsPendingAndNeverClobbersReport(t *testing.T) {
	inputs := discoverFiles(t, "a.pdf", "b.pdf", "c.pdf")
	plan, err := PlanOutputs(inputs, OutputOptions{Existing: "error", Directory: testutil.TempDir(t)})
	require.NoError(t, err)
	var calls atomic.Int32
	client := fakeAnalysisClient{analyze: func(
		context.Context, cuclient.AnalyzeRequest,
	) (cuclient.AnalyzeResponse, error) {
		calls.Add(1)
		require.NoError(t, os.WriteFile(*plan.Outputs[0].Path, []byte("other writer"), 0o600))
		return cuclient.AnalyzeResponse{JSON: json.RawMessage(terminal)}, nil
	}}
	options := runOptions()
	options.Concurrency = 1
	_, err = Execute(t.Context(), client, plan, options)
	requireCode(t, err, "result_write_failed")
	// One next request may already be in flight while the completed result is being published.
	assert.LessOrEqual(t, calls.Load(), int32(2))
	// #nosec G304 -- The result path was planned inside this test's temporary directory.
	body, err := os.ReadFile(*plan.Outputs[0].Path)
	require.NoError(t, err)
	assert.Equal(t, "other writer", string(body))
	reportPath := writeInput(t, testutil.TempDir(t), "report")
	plan, err = PlanOutputs(inputs[:1], OutputOptions{Existing: "error"})
	require.NoError(t, err)
	plan.Report = reportPath
	_, err = Execute(t.Context(), successfulClient(), plan, runOptions())
	requireCode(t, err, "report_write_failed")
	// #nosec G304 -- Reads only this test's private temporary report.
	body, err = os.ReadFile(reportPath)
	require.NoError(t, err)
	assert.Equal(t, "report", string(body))
}

func TestExecutionInvalidResponsesUsageFallbackAndInputReplacement(t *testing.T) {
	inputs := discoverFiles(t, "a.pdf")
	plan, err := PlanOutputs(inputs, OutputOptions{Existing: "error"})
	require.NoError(t, err)
	for _, raw := range []string{"invalid", "null", `{}`, `{"result":null}`, `{"result":[]}`} {
		client := fakeAnalysisClient{analyze: func(
			context.Context, cuclient.AnalyzeRequest,
		) (cuclient.AnalyzeResponse, error) {
			return cuclient.AnalyzeResponse{JSON: json.RawMessage(raw)}, nil
		}}
		result, err := Execute(t.Context(), client, plan, runOptions())
		requireCode(t, err, "analysis_failed")
		assert.Contains(t, result.Records[0].Error, "invalid complete analysis response")
	}
	client := fakeAnalysisClient{analyze: func(context.Context, cuclient.AnalyzeRequest) (cuclient.AnalyzeResponse, error) {
		return cuclient.AnalyzeResponse{JSON: json.RawMessage(`{"result":{}}`),
			Usage: &cuclient.Usage{AudioHours: new(0.0)}}, nil
	}}
	result, err := Execute(t.Context(), client, plan, runOptions())
	require.NoError(t, err)
	assert.JSONEq(t, `{"audioHours":0}`, string(result.Records[0].Usage))
	replacement := writeInput(t, filepath.Dir(inputs[0].Path), "replacement")
	require.NoError(t, os.Rename(replacement, inputs[0].Path))
	result, err = Execute(t.Context(), successfulClient(), plan, runOptions())
	requireCode(t, err, "analysis_failed")
	assert.Contains(t, result.Records[0].Error, "changed after discovery")
}

func TestExecutionLLMFileAndAbsentUsage(t *testing.T) {
	inputs := discoverFiles(t, "a.pdf")
	plan, err := PlanOutputs(inputs, OutputOptions{Existing: "error", Directory: testutil.TempDir(t), LLMInput: true})
	require.NoError(t, err)
	options := runOptions()
	options.Usage = false
	result, err := Execute(t.Context(), successfulClient(), plan, options)
	require.NoError(t, err)
	assert.Equal(t, "null", string(result.Records[0].Usage))
	// #nosec G304 -- The result path was planned inside this test's temporary directory.
	body, err := os.ReadFile(*plan.Outputs[0].Path)
	require.NoError(t, err)
	golden, err := os.ReadFile("testdata/llm.txt")
	require.NoError(t, err)
	assert.Equal(t, strings.TrimSuffix(string(golden), "\n"), string(body))
}

func TestSafeDiagnostic(t *testing.T) {
	assert.Equal(t, "Analysis timed out.", safeDiagnostic(context.DeadlineExceeded))
	assert.NotContains(t, safeDiagnostic(fmt.Errorf("token secret")), "secret")
	assert.Equal(t, "[invalid URL]", RedactURL("https://["))
	assert.Equal(t, "https://example.com/甲乙?REDACTED",
		RedactURL("https://user:private@example.com/甲乙?sig=private#fragment"))
	assert.Equal(t, "https://example.com", RedactURL("https://user:private@example.com#fragment"))
	assert.Equal(t, "https://example.com/a", RedactURL("https://example.com/a?#fragment"))
}

func TestExecutionValidationAndPreview(t *testing.T) {
	inputs := discoverFiles(t, "a.pdf")
	plan, err := PlanOutputs(inputs, OutputOptions{Existing: "error"})
	require.NoError(t, err)
	preview := plan.Preview("analyzer")
	assert.Equal(t, DryRun{DryRun: true, Analyzer: "analyzer", Outputs: []DryRunOutput{
		{Input: inputs[0].Reference},
	}}, preview)
	for _, test := range []struct {
		name    string
		options RunOptions
		code    string
	}{
		{"analyzer", RunOptions{Concurrency: 1, Mode: cuclient.AnalysisModeAsync}, "invalid_analyzer_id"},
		{"concurrency", RunOptions{Analyzer: "valid", Mode: cuclient.AnalysisModeAsync}, "invalid_concurrency"},
		{"mode", RunOptions{Analyzer: "valid", Concurrency: 1}, "invalid_analysis_mode"},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := Execute(t.Context(), successfulClient(), plan, test.options)
			requireCode(t, err, test.code)
		})
	}
	_, err = Execute(t.Context(), successfulClient(), Plan{}, runOptions())
	requireCode(t, err, "empty_inputs")
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err = Execute(ctx, successfulClient(), plan, runOptions())
	require.ErrorIs(t, err, context.Canceled)
	require.Error(t, savePayload(filepath.Join(testutil.TempDir(t), "out"), make(chan int), false))
	inputs[0].Path += ".missing"
	_, _, err = analyzeOne(t.Context(), successfulClient(), inputs[0], false, runOptions())
	requireCode(t, err, "input_read_failed")
	_, _, err = analyzeOne(ctx, successfulClient(), inputs[0], false, runOptions())
	require.ErrorIs(t, err, context.Canceled)
}

func TestDiscoverySpecialPathsAndInvalidGlob(t *testing.T) {
	assert.False(t, isURL(`C:\folder\file.pdf`))
	assert.False(t, isURL(`C:/folder/file.pdf`))
	assert.True(t, isURL("http:missing-file"))
	assert.True(t, isURL("https://example.com/a"))
	if runtime.GOOS != "windows" {
		path := writeInput(t, testutil.TempDir(t), "http:local-file.pdf")
		assert.False(t, isURL(path))
	}
	_, err := Discover(t.Context(), InputOptions{Files: []string{""}})
	requireCode(t, err, "empty_input")
	_, err = globPattern("[z-a]")
	requireCode(t, err, "invalid_pattern")
	assert.True(t, filepath.IsAbs(pathKey("relative", testutil.TempDir(t))))
}
