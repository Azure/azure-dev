// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package analysis

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"

	"azure.ai.content-understanding/internal/cuclient"
	"azure.ai.content-understanding/internal/exterrors"
	"azure.ai.content-understanding/internal/testutil"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExecutionProgressTracksOnlyStartedInputs(t *testing.T) {
	inputs := discoverFiles(t, "a.pdf", "b.pdf", "skipped.pdf")
	plan, err := PlanOutputs(inputs, OutputOptions{Existing: "error"})
	require.NoError(t, err)
	plan.Outputs[2].Skipped = true
	var mu sync.Mutex
	events := map[int][]bool{}
	options := runOptions()
	options.Progress = func(index int, input Input, active bool) error {
		mu.Lock()
		defer mu.Unlock()
		assert.Equal(t, inputs[index], input)
		events[index] = append(events[index], active)
		return nil
	}
	client := fakeAnalysisClient{analyze: func(
		context.Context, cuclient.AnalyzeRequest,
	) (cuclient.AnalyzeResponse, error) {
		return cuclient.AnalyzeResponse{JSON: json.RawMessage(terminal)}, nil
	}}
	_, err = Execute(t.Context(), client, plan, options)
	require.NoError(t, err)
	require.Equal(t, map[int][]bool{0: {true, false}, 1: {true, false}}, events)
}

func TestAnalysisProgressCompletesOnFailureAndCancellation(t *testing.T) {
	for _, cancelled := range []bool{false, true} {
		t.Run(fmt.Sprint(cancelled), func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			input := discoverFiles(t, "invoice.pdf")[0]
			var events []bool
			options := runOptions()
			options.Progress = func(_ int, _ Input, active bool) error {
				events = append(events, active)
				return nil
			}
			want := errors.New("analysis failed")
			if cancelled {
				want = context.Canceled
			}
			client := fakeAnalysisClient{analyze: func(
				context.Context, cuclient.AnalyzeRequest,
			) (cuclient.AnalyzeResponse, error) {
				if cancelled {
					cancel()
				}
				return cuclient.AnalyzeResponse{}, want
			}}
			_, _, err := analyzeWithProgress(ctx, client, input, 0, false, options)
			require.ErrorIs(t, err, want)
			require.Equal(t, []bool{true, false}, events)
		})
	}
}

func TestAnalysisProgressFailurePreventsRequest(t *testing.T) {
	want := errors.New("progress writer failed")
	options := runOptions()
	options.Progress = func(int, Input, bool) error { return want }
	client := fakeAnalysisClient{analyze: func(
		context.Context, cuclient.AnalyzeRequest,
	) (cuclient.AnalyzeResponse, error) {
		t.Error("must not analyze after progress output fails")
		return cuclient.AnalyzeResponse{}, nil
	}}
	_, _, err := analyzeWithProgress(t.Context(), client, Input{}, 0, false, options)
	require.ErrorIs(t, err, want)
}

func TestExecutionFailureDiagnosticsAreActionableAndBounded(t *testing.T) {
	for _, count := range []int{1, 12} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			urls := make([]string, count)
			for index := range urls {
				urls[index] = fmt.Sprintf("https://example.com/private-%d.pdf?sig=private-signature", index)
			}
			inputs, err := Discover(t.Context(), InputOptions{URLs: urls})
			require.NoError(t, err)
			plan, err := PlanOutputs(inputs, OutputOptions{Existing: "error", Directory: testutil.TempDir(t)})
			require.NoError(t, err)
			calls := 0
			client := fakeAnalysisClient{analyze: func(
				context.Context, cuclient.AnalyzeRequest,
			) (cuclient.AnalyzeResponse, error) {
				calls++
				return cuclient.AnalyzeResponse{}, &cuclient.Error{
					Kind: cuclient.ErrorNotFound, Code: "NotFound", StatusCode: 404,
					Message: "The Content Understanding request failed (HTTP 404; code: NotFound).",
					Suggestion: "Verify the analyzer ID. Use azd ai cu analyzer list. " +
						"https://user:private-password@example.com/input?sig=private-secret#private-fragment",
				}
			}}
			options := runOptions()
			options.Concurrency = 1
			result, err := Execute(t.Context(), client, plan, options)
			require.Error(t, err)
			diagnostic := exterrors.FromError(err).Error()
			require.Contains(t, diagnostic, "Input 1:")
			require.Contains(t, diagnostic, "HTTP 404; code: NotFound")
			require.Contains(t, diagnostic, "azd ai cu analyzer list")
			require.NotContains(t, diagnostic, "private")
			require.NotContains(t, diagnostic, "Diagnostic omitted")
			require.Equal(t, count, calls, "diagnostics must not issue another analysis request")
			require.Len(t, result.Records, count)
			if count > 1 {
				require.Contains(t, diagnostic, "additional failed input(s)")
			}
		})
	}
}

func TestExecutionLongFailureStillShowsFirstReason(t *testing.T) {
	inputs := discoverFiles(t, "a.pdf")
	plan, err := PlanOutputs(inputs, OutputOptions{Existing: "error"})
	require.NoError(t, err)
	client := fakeAnalysisClient{analyze: func(
		context.Context, cuclient.AnalyzeRequest,
	) (cuclient.AnalyzeResponse, error) {
		return cuclient.AnalyzeResponse{}, &cuclient.Error{
			Kind: cuclient.ErrorService, Message: "Invalid document. " + strings.Repeat("x", 1800),
		}
	}}
	_, err = Execute(t.Context(), client, plan, runOptions())
	require.Error(t, err)
	diagnostic := exterrors.FromError(err).Error()
	require.Contains(t, diagnostic, "Input 1: Invalid document.")
	require.NotContains(t, diagnostic, "Diagnostic omitted")
}

func TestExecutionInputModeAndUsageContracts(t *testing.T) {
	for _, mode := range []cuclient.AnalysisMode{cuclient.AnalysisModeAsync, cuclient.AnalysisModeInline} {
		for _, remote := range []bool{false, true} {
			for _, usage := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/remote=%t/usage=%t", mode, remote, usage), func(t *testing.T) {
					inputs := discoverFiles(t, "document.pdf")
					const url = "https://example.com/document.pdf?sig=secret#fragment"
					if remote {
						var err error
						inputs, err = Discover(t.Context(), InputOptions{URLs: []string{url}})
						require.NoError(t, err)
					}

					plan, err := PlanOutputs(inputs, OutputOptions{Existing: "error"})
					require.NoError(t, err)
					calls := 0
					var reader io.ReadSeeker
					client := fakeAnalysisClient{analyze: func(
						_ context.Context, request cuclient.AnalyzeRequest,
					) (cuclient.AnalyzeResponse, error) {
						calls++
						require.Equal(t, "prebuilt-invoice", request.AnalyzerID)
						require.Equal(t, mode, request.Mode)
						if remote {
							require.Equal(t, url, request.Input.URL)
							require.Nil(t, request.Input.Body)
						} else {
							require.Empty(t, request.Input.URL)
							reader = request.Input.Body
							require.NotNil(t, reader)
							content, err := io.ReadAll(reader)
							require.NoError(t, err)
							require.Equal(t, "document.pdf", string(content))
							require.Equal(t, "application/octet-stream", request.Input.ContentType)
						}
						return cuclient.AnalyzeResponse{JSON: json.RawMessage(terminal)}, nil
					}}
					options := runOptions()
					options.Mode, options.Usage = mode, usage
					result, err := Execute(t.Context(), client, plan, options)
					require.NoError(t, err)
					require.Equal(t, 1, calls)
					expectedUsage := json.RawMessage("null")
					if usage {
						expectedUsage = json.RawMessage(
							`{"documentPagesStandard":0,"tokens":{"model":4},"future":9007199254740993}`)
					}
					require.Equal(t, []Record{{
						Input: inputs[0].Reference, Status: "succeeded", Analyzer: options.Analyzer,
						Usage: expectedUsage,
					}}, result.Records)
					encoded, err := json.Marshal(result.Stream)
					require.NoError(t, err)
					expected := terminal
					if usage {
						expected = `{"result":` + terminal + `,"usage":` + string(result.Records[0].Usage) + `}`
					}
					require.JSONEq(t, expected, string(encoded))
					require.Contains(t, string(encoded), "9007199254740993")
					require.NotContains(t, inputs[0].Reference, "secret")
					if reader != nil {
						_, err := reader.Read(make([]byte, 1))
						require.Error(t, err, "execution must close its input reader")
					}
				})
			}
		}
	}
}
