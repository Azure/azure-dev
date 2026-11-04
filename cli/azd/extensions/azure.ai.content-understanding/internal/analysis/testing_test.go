// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package analysis

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
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

func TestAnalyzerTestPythonReportParity(t *testing.T) {
	raw, err := os.ReadFile("testdata/analyzer-testing.json")
	require.NoError(t, err)
	var fixture struct {
		SourceCommit string `json:"sourceCommit"`
		Cases        []struct {
			Envelope json.RawMessage `json:"envelope"`
			Fields   json.RawMessage `json:"fields"`
		} `json:"cases"`
		Report json.RawMessage `json:"report"`
	}
	require.NoError(t, json.Unmarshal(raw, &fixture))
	assert.Len(t, fixture.SourceCommit, 40)
	var samples []TestSample
	for i, entry := range fixture.Cases {
		fields, err := testFields(entry.Envelope)
		require.NoError(t, err)
		body, err := json.Marshal(fields)
		require.NoError(t, err)
		assert.JSONEq(t, string(entry.Fields), string(body))
		samples = append(samples, TestSample{Input: "sample-" + strconv.Itoa(i), Status: "ok", Fields: fields})
	}
	samples = append(samples, TestSample{
		Input: "failed-sample", Status: "error", Error: "safe error", Fields: map[string]TestField{},
	})
	body, err := json.Marshal(TestReport{Analyzer: "test-analyzer", Summary: summarizeTest(samples), Samples: samples})
	require.NoError(t, err)
	assert.JSONEq(t, string(fixture.Report), string(body))
	assert.NotContains(t, string(body), "warnings")
	assert.Contains(t, string(body), "9007199254740993")
	assert.Contains(t, string(body), "not a real accuracy benchmark")
}

func TestAnalyzerTestExecutionOrderingConcurrencyAndReaderOwnership(t *testing.T) {
	for _, concurrency := range []int{1, 4, 16} {
		t.Run(strconv.Itoa(concurrency), func(t *testing.T) {
			inputs := discoverFiles(t, "a.pdf", "b.pdf", "c.pdf", "d.pdf")
			var active, peak atomic.Int32
			var mu sync.Mutex
			var bodies []*os.File
			start := make(chan struct{})
			var ready sync.Once
			client := fakeAnalysisClient{analyze: func(
				ctx context.Context, request cuclient.AnalyzeRequest,
			) (cuclient.AnalyzeResponse, error) {
				running := active.Add(1)
				defer active.Add(-1)
				for current := peak.Load(); running > current; current = peak.Load() {
					if peak.CompareAndSwap(current, running) {
						break
					}
				}
				if int(running) == min(concurrency, len(inputs)) {
					ready.Do(func() { close(start) })
				}
				select {
				case <-start:
				case <-ctx.Done():
					return cuclient.AnalyzeResponse{}, ctx.Err()
				}
				assert.Equal(t, "invoice", request.AnalyzerID)
				assert.Equal(t, cuclient.AnalysisModeAsync, request.Mode)
				assert.Empty(t, request.Input.URL)
				file, ok := request.Input.Body.(*os.File)
				require.True(t, ok)
				mu.Lock()
				bodies = append(bodies, file)
				mu.Unlock()
				content, err := io.ReadAll(file)
				require.NoError(t, err)
				if string(content) == "a.pdf" {
					time.Sleep(5 * time.Millisecond)
				}
				if string(content) == "b.pdf" {
					return cuclient.AnalyzeResponse{}, errors.New("private backend secret")
				}
				return cuclient.AnalyzeResponse{JSON: json.RawMessage(terminal)}, ctx.Err()
			}}
			ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
			defer cancel()
			report, err := RunTest(ctx, client, inputs, "invoice", concurrency)
			require.NoError(t, err)
			assert.Equal(t, 4, report.Summary.Total)
			assert.Equal(t, 3, report.Summary.OK)
			assert.Equal(t, 1, report.Summary.Failed)
			assert.Equal(t, 75.0, report.Summary.Fields["Count"].PopulatedPct)
			assert.Equal(t, min(concurrency, len(inputs)), int(peak.Load()))
			for i, sample := range report.Samples {
				assert.Equal(t, inputs[i].Reference, sample.Input)
			}
			assert.NotContains(t, report.Samples[1].Error, "secret")
			for _, file := range bodies {
				_, err := file.Read(make([]byte, 1))
				require.ErrorIs(t, err, os.ErrClosed)
			}
		})
	}
}

func TestAnalyzerTestCancellationAndChangedInputs(t *testing.T) {
	inputs := discoverFiles(t, "a.pdf", "b.pdf", "c.pdf")
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	calls := 0
	client := fakeAnalysisClient{analyze: func(context.Context, cuclient.AnalyzeRequest) (cuclient.AnalyzeResponse, error) {
		calls++
		cancel()
		return cuclient.AnalyzeResponse{JSON: json.RawMessage(terminal)}, nil
	}}
	report, err := RunTest(ctx, client, inputs, "invoice", 1)
	require.ErrorIs(t, err, context.Canceled)
	assert.Equal(t, 1, calls)
	assert.Equal(t, 3, report.Summary.Failed)
	require.NoError(t, os.Remove(inputs[0].Path))
	report, err = RunTest(t.Context(), successfulClient(), inputs[:1], "invoice", 1)
	require.NoError(t, err)
	assert.Equal(t, 1, report.Summary.Failed)
	for _, test := range []struct {
		inputs      []Input
		analyzer    string
		concurrency int
	}{
		{inputs, "bad/id", 4}, {inputs, "invoice", 0}, {inputs, "invoice", 17}, {nil, "invoice", 4},
		{[]Input{{URL: "https://example.com/private?secret"}}, "invoice", 4},
	} {
		_, err := RunTest(t.Context(), successfulClient(), test.inputs, test.analyzer, test.concurrency)
		require.Error(t, err)
	}
	_, err = RunTest(ctx, successfulClient(), inputs, "invoice", 4)
	require.ErrorIs(t, err, context.Canceled)
}

func TestAnalyzerTestMalformedFieldsAndPrivacy(t *testing.T) {
	for _, raw := range []string{
		`{"result":{"contents":[{"fields":42}]}}`,
		`{"result":{"contents":[{"fields":{"Bad":{"valueArray":42}}}]}}`,
		`{"result":{"contents":[{"fields":{"Bad":{"valueArray":[{"valueArray":42}]}}}]}}`,
		`{"result":{"contents":[{"fields":{"Bad":{"valueObject":{"Nested":{"valueArray":42}}}}}]}}`,
	} {
		_, err := testFields(json.RawMessage(raw))
		require.Error(t, err)
	}
	assert.Empty(t, summarizeTest(nil).Fields)
	inputs := discoverFiles(t, "a.pdf")
	report, err := RunTest(t.Context(), fakeAnalysisClient{analyze: func(
		context.Context, cuclient.AnalyzeRequest,
	) (cuclient.AnalyzeResponse, error) {
		return cuclient.AnalyzeResponse{}, &cuclient.Error{Kind: cuclient.ErrorService,
			Message: strings.Repeat("x", 600) + " https://example.com/?secret"}
	}}, inputs, "invoice", 1)
	require.NoError(t, err)
	assert.Len(t, []rune(report.Samples[0].Error), 500)
	assert.NotContains(t, report.Samples[0].Error, "secret")
}

func TestAnalyzerTestReportSafetyAndExplicitOverwrite(t *testing.T) {
	inputs := discoverFiles(t, "a.pdf")
	directory := testutil.TempDir(t)
	path := filepath.Join(directory, "reports", "test.json")
	require.NoError(t, CheckTestReport("", inputs, false))
	require.NoError(t, CheckTestReport(path, inputs, false))
	_, err := os.Stat(filepath.Dir(path))
	require.ErrorIs(t, err, os.ErrNotExist)
	report, err := RunTest(t.Context(), successfulClient(), inputs, "invoice", 1)
	require.NoError(t, err)
	require.NoError(t, SaveTestReport(path, report, false))
	require.Error(t, CheckTestReport(path, inputs, false))
	require.NoError(t, CheckTestReport(path, inputs, true))
	require.Error(t, SaveTestReport(path, report, false))
	require.NoError(t, SaveTestReport(path, report, true))
	require.Error(t, CheckTestReport(inputs[0].Path, inputs, true))
	require.Error(t, CheckTestReport(directory, inputs, true))
	alias := filepath.Join(directory, "hardlink")
	require.NoError(t, os.Link(inputs[0].Path, alias))
	require.Error(t, CheckTestReport(alias, inputs, true))
	require.Error(t, SaveTestReport(directory, report, true))
}
