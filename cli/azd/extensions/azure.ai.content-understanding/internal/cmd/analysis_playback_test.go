// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"azure.ai.content-understanding/internal/analysis"
	"azure.ai.content-understanding/internal/cuclient"
	"azure.ai.content-understanding/internal/testutil"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const recordedBinaryInput = "%PDF-1.7\nsynthetic offline playback input\n%%EOF\n"

var recordedAnalysisCases = []struct {
	name, recording, analyzer string
	interactions              int
	directory                 bool
}{
	{"analyze-single-file-markdown", "analyze_single", "prebuilt-layout", 7, false},
	{"analyze-directory-writes-results", "analyze_batch", "prebuilt-layout", 6, true},
	{"analyze-prebuilt-invoice-json", "analyze_prebuilt_invoice_json", "prebuilt-invoice", 42, false},
}

func TestRecordedAnalysisCommands(t *testing.T) {
	for _, test := range recordedAnalysisCases {
		t.Run(test.name, func(t *testing.T) {
			for _, format := range []string{"default", "json", "none"} {
				t.Run(format, func(t *testing.T) {
					synctest.Test(t, func(t *testing.T) {
						fixture := loadRecordedFixture(t, test.recording)
						require.Len(t, fixture.Interactions, test.interactions)
						require.Equal(t, test.interactions, fixture.Source.InteractionCount)
						require.Empty(t, fixture.Source.UnusedInteractions, "repeated polls must not be dropped")
						transport := &recordedTransport{fixture: fixture, binaryBody: []byte(recordedBinaryInput)}
						root := newRecordedCommand(t, transport, test.analyzer)
						directory := testutil.TempDir(t)
						input := filepath.Join(directory, "sample_invoice.pdf")
						require.NoError(t, os.WriteFile(input, []byte(recordedBinaryInput), 0o600))
						args := []string{"analyze", "--file", input}
						output := filepath.Join(testutil.TempDir(t), "results")
						if test.directory {
							args = []string{"analyze", "--source", directory, "--output-dir", output}
						}
						if format != "json" {
							args = append(args, "--llm-input=false")
						}
						args = append(args, "--analyzer", test.analyzer,
							"-o", format, "--debug", "--no-prompt",
							"--endpoint", "https://sanitized.services.ai.azure.com",
							"--api-version", cuclient.DefaultAPIVersion)
						var stdout, stderr bytes.Buffer
						root.SetOut(&stdout)
						root.SetErr(&stderr)
						require.NoError(t, ValidateInvocation(root, args))
						root.SetArgs(args)
						start := time.Now()
						require.NoError(t, root.ExecuteContext(t.Context()))
						require.NoError(t, transport.complete())
						assert.Equal(t, time.Duration(test.interactions-2)*time.Second, time.Since(start),
							"virtual time preserves every polling interval, not shortened cassette playback")
						assert.Empty(t, stderr.String())
						terminal := fixture.Interactions[len(fixture.Interactions)-1].Response.Body
						wanted, err := recordedJSON(terminal)
						require.NoError(t, err)
						if test.directory {
							output = filepath.Join(output, "sample_invoice.pdf.result.json")
							// #nosec G304 -- Reads only this test's private temporary output.
							body, err := os.ReadFile(output)
							require.NoError(t, err)
							saved, err := recordedJSON(body)
							require.NoError(t, err)
							assert.Equal(t, wanted, saved, "saved result retains the entire recorded envelope")
							wanted = []any{map[string]any{
								"input": input, "status": "succeeded", "analyzerId": test.analyzer,
								"output": output, "usage": nil,
							}}
						}
						if format == "none" {
							assert.Empty(t, stdout.String())
						} else {
							actual, err := recordedJSON(stdout.Bytes())
							require.NoError(t, err)
							assert.Equal(t, wanted, actual, "preserve all fields, warnings, usage and exact numbers")
						}
					})
				})
			}
		})
	}
}

func TestRecordedAnalysisDefaultLLMInput(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		fixture := loadRecordedFixture(t, "analyze_single")
		transport := &recordedTransport{fixture: fixture, binaryBody: []byte(recordedBinaryInput)}
		root := newRecordedCommand(t, transport, "prebuilt-layout")
		input := filepath.Join(testutil.TempDir(t), "invoice.pdf")
		require.NoError(t, os.WriteFile(input, []byte(recordedBinaryInput), 0o600))
		root.SetArgs([]string{"analyze", input, "--analyzer", "prebuilt-layout", "--no-prompt",
			"--endpoint", "https://sanitized.services.ai.azure.com", "--api-version", cuclient.DefaultAPIVersion})
		var stdout, stderr bytes.Buffer
		root.SetOut(&stdout)
		root.SetErr(&stderr)
		require.NoError(t, root.ExecuteContext(t.Context()))
		require.NoError(t, transport.complete())
		assert.Empty(t, stderr.String())
		var envelope struct {
			Result json.RawMessage `json:"result"`
		}
		require.NoError(t, json.Unmarshal(fixture.Interactions[len(fixture.Interactions)-1].Response.Body, &envelope))
		expected, err := analysis.RenderLLM(envelope.Result)
		require.NoError(t, err)
		assert.Equal(t, expected+"\n", stdout.String())
		assert.False(t, json.Valid(stdout.Bytes()))
	})
}

func TestRecordedAnalysisSyntheticURLContract(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		fixture := loadRecordedFixture(t, "analyze_single")
		first := &fixture.Interactions[0]
		require.Equal(t, "scrubbed-binary", first.Request.BodyMode)
		first.Request.URL = strings.Replace(first.Request.URL, ":analyzeBinary?", ":analyze?", 1)
		first.Request.BodyMode = "json"
		first.Request.Body = json.RawMessage(`{"inputs":[{"url":"https://example.com/sample_invoice.pdf"}]}`)
		first.Request.Headers["Content-Type"] = "application/json"
		transport := &recordedTransport{fixture: fixture}
		root := newRecordedCommand(t, transport, "prebuilt-layout")
		args := []string{"analyze", "--url", "https://example.com/sample_invoice.pdf",
			"--analyzer", "prebuilt-layout", "-o", "json", "--no-prompt",
			"--endpoint", "https://sanitized.services.ai.azure.com", "--api-version", cuclient.DefaultAPIVersion}
		var stdout, stderr bytes.Buffer
		root.SetOut(&stdout)
		root.SetErr(&stderr)
		require.NoError(t, ValidateInvocation(root, args))
		root.SetArgs(args)
		require.NoError(t, root.ExecuteContext(t.Context()))
		assert.Empty(t, stderr.String())
		actual, err := recordedJSON(stdout.Bytes())
		require.NoError(t, err)
		wanted, err := recordedJSON(fixture.Interactions[len(fixture.Interactions)-1].Response.Body)
		require.NoError(t, err)
		assert.Equal(t, wanted, actual, "synthetic URL contract reuses recorded responses, not URL service evidence")
	})
}

func TestRecordedAnalysisSyntheticPollingFailures(t *testing.T) {
	for _, name := range []string{
		"wrong-operation", "cross-origin", "wrong-version", "extra-query",
		"wrong-terminal-id", "extra-poll", "unconsumed-polls", "binary-bytes", "missing-binary-expectation",
	} {
		t.Run(name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				fixture := loadRecordedFixture(t, "analyze_single")
				last := len(fixture.Interactions) - 1
				location := fixture.Interactions[0].Response.Headers["Operation-Location"]
				switch name {
				case "wrong-operation":
					location = strings.Replace(location, "/analyzerResults/", "/analyzerResults/other-", 1)
				case "cross-origin":
					location = strings.Replace(location, "sanitized.services.ai.azure.com", "example.com", 1)
				case "wrong-version":
					location = strings.Replace(location, cuclient.DefaultAPIVersion, cuclient.PreviewAPIVersion, 1)
				case "extra-query":
					location += "&unexpected=synthetic"
				case "wrong-terminal-id":
					var body map[string]json.RawMessage
					require.NoError(t, json.Unmarshal(fixture.Interactions[last].Response.Body, &body))
					body["id"] = json.RawMessage(`"different-operation"`)
					updated, err := json.Marshal(body)
					require.NoError(t, err)
					fixture.Interactions[last].Response.Body = updated
				case "extra-poll":
					fixture.Interactions[last].Response = fixture.Interactions[1].Response
				case "unconsumed-polls":
					fixture.Interactions[1].Response = fixture.Interactions[last].Response
				}
				fixture.Interactions[0].Response.Headers["Operation-Location"] = location
				transport := &recordedTransport{fixture: fixture, binaryBody: []byte(recordedBinaryInput)}
				switch name {
				case "binary-bytes":
					transport.binaryBody = []byte("different synthetic input")
				case "missing-binary-expectation":
					transport.binaryBody = nil
				}
				client, err := cuclient.NewRESTTransport("https://sanitized.services.ai.azure.com",
					&recordedCredential{}, &cuclient.TransportOptions{Transport: transport})
				require.NoError(t, err)
				_, err = client.Analyze(t.Context(), cuclient.AnalyzeRequest{
					AnalyzerID: "prebuilt-layout", Mode: cuclient.AnalysisModeAsync,
					Input: cuclient.Input{
						Body: strings.NewReader(recordedBinaryInput), ContentType: "application/octet-stream",
					},
				})
				if name == "unconsumed-polls" {
					require.NoError(t, err)
					require.ErrorContains(t, transport.complete(), "unconsumed")
					assert.Equal(t, 2, transport.next)
				} else {
					require.Error(t, err, "altered recordings are synthetic failure contracts")
					if name == "wrong-terminal-id" {
						require.NoError(t, transport.complete())
					} else {
						require.Error(t, transport.complete())
					}
				}
			})
		})
	}
}
