// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package analysis

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"testing"

	"azure.ai.content-understanding/internal/cuclient"

	"github.com/stretchr/testify/require"
)

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
