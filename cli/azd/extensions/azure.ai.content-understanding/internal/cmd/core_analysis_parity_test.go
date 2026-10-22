// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"azure.ai.content-understanding/internal/analysis"
	"azure.ai.content-understanding/internal/cuclient"

	"github.com/stretchr/testify/require"
)

type coreAnalysisStep struct {
	Reference string                `json:"reference"`
	Path      string                `json:"path"`
	URL       string                `json:"url"`
	BodyHex   *string               `json:"body_hex"`
	Analyzer  string                `json:"analyzer"`
	Mode      cuclient.AnalysisMode `json:"mode"`
	Response  json.RawMessage       `json:"response"`
	Usage     *cuclient.Usage       `json:"usage"`
	Error     *struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error"`
}

type coreAnalysisNative struct {
	Kind        string             `json:"kind"`
	Inputs      json.RawMessage    `json:"inputs"`
	Directory   string             `json:"directory"`
	LLMInput    bool               `json:"llm_input"`
	Plan        json.RawMessage    `json:"plan"`
	Error       *discoveryError    `json:"error"`
	Result      json.RawMessage    `json:"result"`
	Steps       []coreAnalysisStep `json:"steps"`
	Batch       bool               `json:"batch"`
	Usage       bool               `json:"usage"`
	Concurrency int                `json:"concurrency"`
}

type coreAnalysisCall struct {
	Operation   string             `json:"operation"`
	Input       json.RawMessage    `json:"input"`
	InputAfter  json.RawMessage    `json:"input_after"`
	Filesystem  []discoveryFile    `json:"filesystem"`
	Result      json.RawMessage    `json:"result"`
	ResultType  string             `json:"result_type"`
	Error       json.RawMessage    `json:"error"`
	ClientCalls []json.RawMessage  `json:"client_calls"`
	RunnerCalls []json.RawMessage  `json:"runner_calls"`
	Callbacks   []json.RawMessage  `json:"callbacks"`
	Native      coreAnalysisNative `json:"native"`
}

type coreAnalysisOracle struct {
	SchemaVersion int    `json:"schema_version"`
	Role          string `json:"role"`
	Source        struct {
		Repository string            `json:"repository"`
		Commit     string            `json:"commit"`
		License    string            `json:"license"`
		Python     string            `json:"python"`
		Platform   string            `json:"platform"`
		Files      map[string]string `json:"files"`
		CaseCount  int               `json:"case_count"`
		CallCount  int               `json:"call_count"`
	} `json:"source"`
	Boundaries []string `json:"boundaries"`
	Cases      []struct {
		SourceCase string             `json:"source_case"`
		Calls      []coreAnalysisCall `json:"calls"`
	} `json:"cases"`
}

const coreAnalysisGoTest = "internal/cmd/core_analysis_parity_test.go::TestCoreAnalysisParity"

func loadCoreAnalysisOracle(t *testing.T) coreAnalysisOracle {
	t.Helper()
	inventory := loadParityInventory(t)
	require.Equal(t, "testdata/parity/core_analysis.json", inventory.CoreAnalysis.File)
	body, err := os.ReadFile("../../testdata/parity/core_analysis.json")
	require.NoError(t, err)
	require.Equal(t, inventory.CoreAnalysis.SHA256, sha256Text(body))
	_, err = recordedJSON(body)
	require.NoError(t, err)
	var oracle coreAnalysisOracle
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	require.NoError(t, decoder.Decode(&oracle))
	require.Equal(t, 1, oracle.SchemaVersion)
	require.Equal(t, "public-source-analysis-controls-not-service-recordings", oracle.Role)
	require.Equal(t, inventory.Source.Repository, oracle.Source.Repository)
	require.Equal(t, paritySourceCommit, oracle.Source.Commit)
	require.Equal(t, "MIT", oracle.Source.License)
	require.Equal(t, "Linux", oracle.Source.Platform)
	require.NotEmpty(t, oracle.Source.Python)
	require.Len(t, oracle.Source.Files, 7)
	for _, path := range []string{
		"packages/core/tests/test_analysis.py",
		"packages/core/src/cu_cli_core/analysis.py",
		"packages/core/src/cu_cli_core/operations/analysis.py",
		"packages/core/src/cu_cli_core/contracts.py",
		"packages/core/src/cu_cli_core/input_planning.py",
		"packages/core/src/cu_cli_core/errors.py",
		"packages/core/src/cu_cli_core/serialization.py",
	} {
		require.Regexp(t, "^[0-9a-f]{64}$", oracle.Source.Files[path])
	}
	require.Equal(t, 29, oracle.Source.CaseCount)
	require.Equal(t, 42, oracle.Source.CallCount)
	require.Len(t, oracle.Boundaries, 4)
	require.Len(t, oracle.Cases, oracle.Source.CaseCount)
	mapped := map[string]bool{}
	for _, row := range inventory.Cases {
		if slices.ContainsFunc(row.GoTests, func(ref string) bool {
			return strings.HasPrefix(ref, coreAnalysisGoTest+"/")
		}) {
			require.Contains(t, []string{"evidenced", "approved"}, row.State)
			require.Equal(t, "difference", row.Disposition)
			require.NoError(t, parityState(row.State, row.Approval))
			require.Contains(t, row.GoTests, coreAnalysisGoTest+"/"+sourceParitySubtest(row.Source))
			mapped[row.Source] = true
		}
	}
	require.Len(t, mapped, 29)
	calls, failures, steps := 0, 0, 0
	for _, row := range oracle.Cases {
		require.True(t, mapped[row.SourceCase])
		require.True(t, strings.HasPrefix(row.SourceCase, "packages/core/tests/test_analysis.py::"))
		delete(mapped, row.SourceCase)
		require.NotEmpty(t, row.Calls)
		for _, call := range row.Calls {
			calls++
			require.NotEmpty(t, call.Operation)
			require.NotEmpty(t, call.Input)
			require.NotEmpty(t, call.InputAfter)
			require.Contains(t, []string{"plan", "dedupe", "execute"}, call.Native.Kind)
			if string(call.Error) != "null" {
				failures++
				require.Equal(t, "plan_jobs", call.Operation)
				require.Empty(t, call.ResultType)
				require.Equal(t, "null", string(call.Result))
			} else {
				require.NotEmpty(t, call.ResultType)
			}
			steps += len(call.Native.Steps)
		}
	}
	require.Empty(t, mapped)
	require.Equal(t, 42, calls)
	require.Equal(t, 1, failures)
	require.Equal(t, 39, steps)
	return oracle
}

func coreAnalysisPath(root, value string) string {
	return strings.ReplaceAll(value, "$TMP", filepath.ToSlash(root))
}

func coreAnalysisNormalize(root, value string) string {
	return strings.ReplaceAll(filepath.ToSlash(value), filepath.ToSlash(root), "$TMP")
}

func assertCoreAnalysisError(t *testing.T, expected *discoveryError, err error) {
	t.Helper()
	if expected == nil {
		require.NoError(t, err)
		return
	}
	var domain *cuclient.Error
	require.ErrorAs(t, err, &domain)
	require.Equal(t, *expected, discoveryError{
		Kind: string(domain.Kind), Code: domain.Code, Message: domain.Message,
		Hint: domain.Suggestion, StatusCode: domain.StatusCode,
	})
}

func coreAnalysisInputs(inputs []analysis.Input, root string) []outputPlanningInput {
	result := make([]outputPlanningInput, 0, len(inputs))
	for _, input := range inputs {
		result = append(result, outputPlanningInput{
			Path: coreAnalysisNormalize(root, input.Path), URL: input.URL,
			Reference: coreAnalysisNormalize(root, input.Reference), Relative: filepath.ToSlash(input.Relative),
		})
	}
	return result
}

func replayCoreAnalysisPlan(t *testing.T, root string, native coreAnalysisNative) {
	t.Helper()
	var inputs []outputPlanningInput
	require.NoError(t, json.Unmarshal(native.Inputs, &inputs))
	typed := make([]analysis.Input, 0, len(inputs))
	for _, input := range inputs {
		typed = append(typed, analysis.Input{
			Reference: coreAnalysisPath(root, input.Reference), Path: coreAnalysisPath(root, input.Path),
			URL: input.URL, Relative: filepath.FromSlash(input.Relative),
		})
	}
	directory := coreAnalysisPath(root, native.Directory)
	plan, err := analysis.PlanOutputs(typed, analysis.OutputOptions{
		Directory: directory, Existing: "error", LLMInput: native.LLMInput,
	})
	assertCoreAnalysisError(t, native.Error, err)
	if err != nil {
		require.Equal(t, analysis.Plan{}, plan)
		return
	}
	actual := outputPlanningResult{
		Outputs: []outputPlanningOutput{}, Existing: plan.Existing, Report: plan.Report, LLMInput: plan.LLMInput,
	}
	for _, output := range plan.Outputs {
		row := outputPlanningOutput{
			Input:  coreAnalysisInputs([]analysis.Input{output.Input}, root)[0],
			Exists: output.Exists, Skipped: output.Skipped,
		}
		if output.Path != nil {
			row.Path = new(coreAnalysisNormalize(root, *output.Path))
		}
		actual.Outputs = append(actual.Outputs, row)
	}
	assertNormalizedJSON(t, native.Plan, actual)
}

func replayCoreAnalysisDedupe(t *testing.T, root string, native coreAnalysisNative) {
	t.Helper()
	var refs []string
	require.NoError(t, json.Unmarshal(native.Inputs, &refs))
	for index, ref := range refs {
		if strings.HasPrefix(ref, "$TMP") {
			refs[index] = coreAnalysisPath(root, ref)
		} else {
			refs[index] = filepath.ToSlash(root) + "/" + ref
		}
	}
	inputs, err := analysis.Discover(t.Context(), analysis.InputOptions{Files: refs})
	assertCoreAnalysisError(t, native.Error, err)
	if err != nil {
		require.Nil(t, inputs)
		return
	}
	assertNormalizedJSON(t, native.Result, coreAnalysisInputs(inputs, root))
}

type coreAnalysisRequest struct {
	Analyzer    string                `json:"analyzer"`
	Mode        cuclient.AnalysisMode `json:"mode"`
	URL         string                `json:"url"`
	BodyHex     *string               `json:"body_hex"`
	ContentType string                `json:"content_type"`
}

type coreAnalysisObserver struct {
	mu       sync.Mutex
	seen     []bool
	requests []coreAnalysisRequest
	readers  []*os.File
	active   atomic.Int32
	peak     atomic.Int32
	arrivals atomic.Int32
	release  chan struct{}
	limit    int
	steps    []coreAnalysisStep
}

func (o *coreAnalysisObserver) analyze(
	ctx context.Context, request cuclient.AnalyzeRequest,
) (cuclient.AnalyzeResponse, error) {
	active := o.active.Add(1)
	defer o.active.Add(-1)
	for peak := o.peak.Load(); active > peak; peak = o.peak.Load() {
		if o.peak.CompareAndSwap(peak, active) {
			break
		}
	}
	if int(o.arrivals.Add(1)) == o.limit {
		close(o.release)
	}
	select {
	case <-o.release:
	case <-ctx.Done():
		return cuclient.AnalyzeResponse{}, ctx.Err()
	}
	observed := coreAnalysisRequest{
		Analyzer: request.AnalyzerID, Mode: request.Mode, URL: request.Input.URL,
		ContentType: request.Input.ContentType,
	}
	var reader *os.File
	if request.Input.Body != nil {
		body, err := io.ReadAll(request.Input.Body)
		if err != nil {
			return cuclient.AnalyzeResponse{}, err
		}
		observed.BodyHex = new(hex.EncodeToString(body))
		var ok bool
		reader, ok = request.Input.Body.(*os.File)
		if !ok {
			return cuclient.AnalyzeResponse{}, fmt.Errorf("expected production file reader")
		}
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	o.requests = append(o.requests, observed)
	if reader != nil {
		o.readers = append(o.readers, reader)
	}
	for index, step := range o.steps {
		contentType := ""
		if step.BodyHex != nil {
			contentType = "application/octet-stream"
		}
		if o.seen[index] || step.Analyzer != observed.Analyzer || step.Mode != observed.Mode ||
			step.URL != observed.URL || contentType != observed.ContentType ||
			!sameOptionalText(step.BodyHex, observed.BodyHex) {
			continue
		}
		o.seen[index] = true
		if step.Error != nil {
			return cuclient.AnalyzeResponse{}, fmt.Errorf("source runner failure: %s", step.Error.Message)
		}
		return cuclient.AnalyzeResponse{JSON: step.Response, Usage: step.Usage}, nil
	}
	return cuclient.AnalyzeResponse{}, fmt.Errorf("unexpected or repeated native analysis request")
}

func sameOptionalText(left, right *string) bool {
	return left == nil && right == nil || left != nil && right != nil && *left == *right
}

func coreAnalysisExecutionInputs(
	t *testing.T, root string, fixture *os.Root, steps []coreAnalysisStep,
) []analysis.Input {
	t.Helper()
	inputs := make([]analysis.Input, 0, len(steps))
	for index, step := range steps {
		if step.URL != "" {
			selected, err := analysis.Discover(t.Context(), analysis.InputOptions{URLs: []string{step.URL}})
			require.NoError(t, err)
			require.Len(t, selected, 1)
			require.Equal(t, step.Reference, selected[0].Reference)
			selected[0].Relative = fmt.Sprintf("%d.input", index)
			inputs = append(inputs, selected[0])
			continue
		}
		require.NotNil(t, step.BodyHex)
		require.True(t, strings.HasPrefix(step.Path, "$TMP/"))
		relative := strings.TrimPrefix(step.Path, "$TMP/")
		discoveryPath(t, root, relative)
		content, err := hex.DecodeString(*step.BodyHex)
		require.NoError(t, err)
		existing, err := fixture.ReadFile(relative)
		if errors.Is(err, os.ErrNotExist) {
			require.NoError(t, fixture.MkdirAll(filepath.Dir(relative), 0o700))
			require.NoError(t, fixture.WriteFile(relative, content, 0o600))
		} else {
			require.NoError(t, err)
			require.Equal(t, content, existing, "original source input bytes must not change")
		}
		input, err := analysis.DiscoverFile(t.Context(), coreAnalysisPath(root, step.Path))
		require.NoError(t, err)
		input.Reference = coreAnalysisPath(root, step.Reference)
		input.Relative = fmt.Sprintf("%d.input", index)
		inputs = append(inputs, input)
	}
	return inputs
}

func replayCoreAnalysisExecution(t *testing.T, root string, fixture *os.Root, native coreAnalysisNative) {
	t.Helper()
	require.NotEmpty(t, native.Steps)
	inputs := coreAnalysisExecutionInputs(t, root, fixture, native.Steps)
	directory := ""
	if native.Batch {
		directory = filepath.Join(root, "native-results")
	}
	plan, err := analysis.PlanOutputs(inputs, analysis.OutputOptions{Directory: directory, Existing: "error"})
	require.NoError(t, err)
	before := discoverySnapshot(t, fixture)
	observer := &coreAnalysisObserver{
		steps: native.Steps, seen: make([]bool, len(native.Steps)),
		limit: min(native.Concurrency, len(native.Steps)), release: make(chan struct{}),
	}
	client := &analyzeFake{call: observer.analyze}
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	first := native.Steps[0]
	actual, err := analysis.Execute(ctx, client, plan, analysis.RunOptions{
		Analyzer: first.Analyzer, Mode: first.Mode, Concurrency: native.Concurrency, Usage: native.Usage,
	})
	assertCoreAnalysisError(t, native.Error, err)
	require.EqualValues(t, len(native.Steps), client.calls.Load())
	require.EqualValues(t, observer.limit, observer.peak.Load(), "native-only first-wave concurrency barrier")
	require.Zero(t, observer.active.Load())
	for _, seen := range observer.seen {
		require.True(t, seen, "every source-derived native request must be consumed exactly once")
	}
	for _, reader := range observer.readers {
		_, err := reader.Stat()
		require.ErrorIs(t, err, os.ErrClosed, "production execution owns and closes local readers")
	}
	require.Len(t, observer.requests, len(native.Steps))
	var expected struct {
		Records []analysis.Record `json:"records"`
		Stream  json.RawMessage   `json:"stream"`
		Saved   []struct {
			Path string          `json:"path"`
			Body json.RawMessage `json:"body"`
		} `json:"saved"`
	}
	require.NoError(t, json.Unmarshal(native.Result, &expected))
	for index := range actual.Records {
		record := &actual.Records[index]
		record.Input = coreAnalysisNormalize(root, record.Input)
		if record.Output != nil {
			record.Output = new(coreAnalysisNormalize(root, *record.Output))
		}
	}
	assertNormalizedJSON(t, expected.Records, actual.Records)
	assertNormalizedJSON(t, expected.Stream, actual.Stream)
	after := slices.Clone(before)
	if len(expected.Saved) != 0 {
		after = append(after, discoveryFile{Path: "native-results", Kind: "directory"})
	}
	for _, saved := range expected.Saved {
		path := strings.TrimPrefix(saved.Path, "$TMP/")
		require.True(t, strings.HasPrefix(path, "native-results/"))
		content, err := fixture.ReadFile(path)
		require.NoError(t, err)
		var payload json.RawMessage
		require.NoError(t, json.Unmarshal(content, &payload))
		assertNormalizedJSON(t, saved.Body, payload)
		formatted, err := json.MarshalIndent(saved.Body, "", "  ")
		require.NoError(t, err)
		body := append(formatted, '\n')
		require.Equal(t, string(body), string(content))
		after = append(after, discoveryFile{
			Path: path, Kind: "file", Content: string(body), SHA256: sha256Text(body), Size: int64(len(body)),
		})
	}
	slices.SortFunc(after, func(left, right discoveryFile) int { return strings.Compare(left.Path, right.Path) })
	require.Equal(t, after, discoverySnapshot(t, fixture), "only declared native result files may be added")
}

func TestCoreAnalysisParity(t *testing.T) {
	for _, row := range loadCoreAnalysisOracle(t).Cases {
		t.Run(sourceParitySubtest(row.SourceCase), func(t *testing.T) {
			for index, call := range row.Calls {
				t.Run(fmt.Sprintf("call-%d", index+1), func(t *testing.T) {
					root, err := filepath.EvalSymlinks(t.TempDir())
					require.NoError(t, err)
					fixture := materializeDiscovery(t, root, call.Filesystem)
					t.Chdir(root)
					require.Equal(t, call.Filesystem, discoverySnapshot(t, fixture))
					switch call.Native.Kind {
					case "plan":
						replayCoreAnalysisPlan(t, root, call.Native)
					case "dedupe":
						replayCoreAnalysisDedupe(t, root, call.Native)
					case "execute":
						replayCoreAnalysisExecution(t, root, fixture, call.Native)
						return
					default:
						t.Fatalf("unsupported native replacement %q", call.Native.Kind)
					}
					require.Equal(t, call.Filesystem, discoverySnapshot(t, fixture))
				})
			}
		})
	}
}

func TestCoreAnalysisControlsAreNotRecordings(t *testing.T) {
	loadCoreAnalysisOracle(t)
	body, err := os.ReadFile("../../testdata/parity/core_analysis.json")
	require.NoError(t, err)
	require.Error(t, recordingHygiene(body), "public synthetic controls must not become service recordings")
	for _, recording := range loadParityInventory(t).Recordings {
		require.NotEqual(t, "testdata/parity/core_analysis.json", recording.File)
	}
}
