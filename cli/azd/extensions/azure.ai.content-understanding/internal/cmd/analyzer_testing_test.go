// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"azure.ai.content-understanding/internal/analysis"
	"azure.ai.content-understanding/internal/config"
	"azure.ai.content-understanding/internal/cuclient"
	"azure.ai.content-understanding/internal/testutil"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func executeAnalyzerTest(f *analyzeFixture, ctx context.Context, args ...string) (string, error) {
	root := f.root()
	var stdout, stderr bytes.Buffer
	root.SetOut(&stdout)
	root.SetErr(&stderr)
	root.SetArgs(append([]string{"analyzer", "test"}, args...))
	err := root.ExecuteContext(ctx)
	return stdout.String(), err
}

func TestAnalyzerTestOfflineDryRunAndLocalInputModes(t *testing.T) {
	for _, mode := range []string{"positional", "named", "alias", "source"} {
		t.Run(mode, func(t *testing.T) {
			f := newAnalyzeFixture(t)
			t.Setenv("AZD_SERVER", "")
			t.Setenv("AZD_ACCESS_TOKEN", "")
			t.Setenv("CU_API_KEY", "private-key-must-not-be-read")
			sample := schemaFile(t, "sample")
			output := filepath.Join(testutil.TempDir(t), "new-directory", "report.json")
			args := []string{"invoice", sample, "--dry-run", "--output-file", output}
			switch mode {
			case "named":
				args = []string{"-n", "invoice", "--file", sample, "--dry-run"}
			case "alias":
				args = []string{"-a", "invoice", sample, "--dry-run"}
			case "source":
				args = []string{"invoice", "--source", filepath.Dir(sample), "--pattern", "*.json", "-r", "--dry-run"}
			}
			stdout, err := executeAnalyzerTest(f, t.Context(), args...)
			require.NoError(t, err)
			expected, _ := json.Marshal(map[string]any{"dryRun": true, "analyzerId": "invoice", "inputs": []string{sample}})
			assert.JSONEq(t, string(expected), stdout)
			assert.Zero(t, f.opens)
			assert.Zero(t, f.client.calls.Load())
			_, err = os.Stat(filepath.Dir(output))
			require.ErrorIs(t, err, os.ErrNotExist)
		})
	}
}

func TestAnalyzerTestCommandMappingReportAndOutput(t *testing.T) {
	for _, format := range []string{"default", "json", "none"} {
		t.Run(format, func(t *testing.T) {
			f := newAnalyzeFixture(t)
			sample := schemaFile(t, "sample bytes")
			output := filepath.Join(testutil.TempDir(t), "report.json")
			var reader io.ReadSeeker
			f.client.call = func(_ context.Context, request cuclient.AnalyzeRequest) (cuclient.AnalyzeResponse, error) {
				assert.Equal(t, "invoice", request.AnalyzerID)
				assert.Equal(t, cuclient.AnalysisModeAsync, request.Mode)
				assert.Empty(t, request.Input.URL)
				reader = request.Input.Body
				body, err := io.ReadAll(reader)
				require.NoError(t, err)
				assert.Equal(t, "sample bytes", string(body))
				return cuclient.AnalyzeResponse{JSON: json.RawMessage(`{"result":{"contents":[{"fields":` +
					`{"Amount":{"valueNumber":0,"confidence":0.6}}}],"warnings":[{"code":"Ignored"}]}}`)}, nil
			}
			stdout, err := executeAnalyzerTest(f, t.Context(), "-n", "invoice", "--file", sample,
				"--output-file", output, "--endpoint", "https://explicit.services.ai.azure.com",
				"--api-version", cuclient.PreviewAPIVersion, "-j", "16", "-o", format)
			require.NoError(t, err)
			assert.Equal(t, "invoice", f.options.DefaultAnalyzer)
			assert.Equal(t, "https://explicit.services.ai.azure.com", f.options.Endpoint)
			assert.Equal(t, cuclient.PreviewAPIVersion, f.options.APIVersion)
			assert.Equal(t, 1, f.closes)
			body, err := os.ReadFile(output) // #nosec G304 -- Reads only this test's private temporary file.
			require.NoError(t, err)
			var report analysis.TestReport
			require.NoError(t, json.Unmarshal(body, &report))
			assert.Equal(t, 1, report.Summary.OK)
			assert.Equal(t, 100.0, report.Summary.Fields["Amount"].PopulatedPct)
			assert.Zero(t, report.Summary.Fields["Amount"].LowConfidenceCount)
			assert.NotContains(t, string(body), "warnings")
			if format == "none" {
				assert.Empty(t, stdout)
			} else {
				assert.JSONEq(t, string(body), stdout)
			}
			_, err = reader.Read(make([]byte, 1))
			require.ErrorIs(t, err, os.ErrClosed)
		})
	}
}

func TestAnalyzerTestInvalidInputAndReportPoliciesBeforeHost(t *testing.T) {
	f := newAnalyzeFixture(t)
	sample := schemaFile(t, "sample")
	for _, args := range [][]string{
		{}, {"invoice"}, {"bad/id", sample}, {"invoice", sample, "--dry-run", "--yes"},
		{"invoice", sample, "-j", "0"}, {"invoice", sample, "-j", "17"},
		{"invoice", "https://example.com/?secret"}, {"invoice", sample, "--url", "https://example.com"},
		{"invoice", sample, "--file", sample}, {"invoice", "--source", sample},
		{"invoice", sample, "--pattern", "*.pdf"}, {"invoice", sample, "--output-file", ""},
		{"invoice", sample, "--output-file", sample, "--force"},
		{"invoice", sample, "--output-file", filepath.Dir(sample), "--force"},
		{"invoice", sample, "--inline"}, {"invoice", sample, "--usage"}, {"invoice", sample, "--json"},
	} {
		stdout, err := executeAnalyzerTest(f, t.Context(), args...)
		require.Error(t, err, "%v", args)
		assert.Empty(t, stdout)
		assert.Zero(t, f.opens)
	}
	output := schemaFile(t, "keep original")
	_, err := executeAnalyzerTest(f, t.Context(), "invoice", sample, "--output-file", output, "--dry-run")
	require.Error(t, err)
	assert.Zero(t, f.opens)
	stdout, err := executeAnalyzerTest(f, t.Context(), "invoice", sample, "--output-file", output, "--force", "-o", "none")
	require.NoError(t, err)
	assert.Empty(t, stdout)
}

func TestAnalyzerTestBillingConfirmationThresholdAndFailures(t *testing.T) {
	for _, mode := range []string{"50 no prompt", "51 blocked", "51 declined", "51 accepted", "51 yes", "prompt error"} {
		t.Run(mode, func(t *testing.T) {
			f := newAnalyzeFixture(t)
			directory := testutil.TempDir(t)
			count := 51
			if mode == "50 no prompt" {
				count = 50
			}
			for i := range count {
				path := filepath.Join(directory, fmt.Sprintf("%02d.pdf", i))
				require.NoError(t, os.WriteFile(path, []byte("sample"), 0o600))
			}
			args := []string{"invoice", "--source", directory}
			switch mode {
			case "50 no prompt", "51 blocked":
				args = append(args, "--no-prompt")
			case "51 declined":
				f.confirmed = false
			case "51 yes":
				args = append(args, "--yes", "--no-prompt")
			case "prompt error":
				f.promptErr = errors.New("private host secret")
			}
			stdout, err := executeAnalyzerTest(f, t.Context(), args...)
			if mode == "51 blocked" || mode == "51 declined" || mode == "prompt error" {
				require.Error(t, err)
				assert.Empty(t, stdout)
				assert.Zero(t, f.client.calls.Load())
				assert.NotContains(t, err.Error(), "secret")
			} else {
				require.NoError(t, err)
				assert.Equal(t, int32(count), f.client.calls.Load())
			}
			if mode == "51 blocked" {
				assert.Zero(t, f.opens)
			}
			if mode == "51 accepted" || mode == "51 declined" || mode == "prompt error" {
				assert.Len(t, f.prompts, 1)
			}
		})
	}
}

func TestAnalyzerTestPartialFailureCancellationAndPublicationRace(t *testing.T) {
	for _, mode := range []string{
		"service failure", "cancelled during request", "report race", "open failure", "writer failure",
	} {
		t.Run(mode, func(t *testing.T) {
			f := newAnalyzeFixture(t)
			sample := schemaFile(t, "sample")
			output := filepath.Join(testutil.TempDir(t), "report.json")
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			f.client.call = func(context.Context, cuclient.AnalyzeRequest) (cuclient.AnalyzeResponse, error) {
				switch mode {
				case "service failure":
					return cuclient.AnalyzeResponse{}, errors.New("private backend secret")
				case "cancelled during request":
					cancel()
					return cuclient.AnalyzeResponse{}, context.Canceled
				case "report race":
					require.NoError(t, os.WriteFile(output, []byte("keep concurrent file"), 0o600))
				}
				return cuclient.AnalyzeResponse{JSON: json.RawMessage(analyzeEnvelope)}, nil
			}
			if mode == "open failure" {
				f.openErr = errors.New("private host secret")
			}
			var err error
			var stdout string
			if mode == "writer failure" {
				root := f.root()
				root.SetOut(failingWriter{})
				root.SetErr(io.Discard)
				root.SetArgs([]string{"analyzer", "test", "invoice", sample, "--output-file", output})
				err = root.ExecuteContext(ctx)
			} else {
				stdout, err = executeAnalyzerTest(f, ctx, "invoice", sample, "--output-file", output)
			}
			require.Error(t, err)
			assert.Empty(t, stdout)
			assert.NotContains(t, err.Error(), "secret")
			if mode != "open failure" {
				body, readErr := os.ReadFile(output) // #nosec G304 -- Reads only this test's private temporary file.
				require.NoError(t, readErr)
				if mode == "report race" {
					assert.Equal(t, "keep concurrent file", string(body))
				} else {
					assert.NotContains(t, string(body), "secret")
					assert.Contains(t, string(body), `"samplesTotal": 1`)
				}
				assert.Equal(t, 1, f.closes)
			}
		})
	}
}

func TestAnalyzerTestWirePipelineForBothAPIVersions(t *testing.T) {
	for _, version := range []string{cuclient.DefaultAPIVersion, cuclient.PreviewAPIVersion} {
		t.Run(version, func(t *testing.T) {
			f := newAnalyzeFixture(t)
			sample := schemaFile(t, "wire sample")
			var calls []string
			client, err := cuclient.NewRESTTransport("https://resource.services.ai.azure.com", &copyWireCredential{},
				&cuclient.TransportOptions{APIVersion: version, Transport: copyWireTransport(func(
					request *http.Request,
				) (*http.Response, error) {
					calls = append(calls, request.Method+" "+request.URL.Path)
					assert.Equal(t, version, request.URL.Query().Get("api-version"))
					assert.Equal(t, "Bearer private-wire-token", request.Header.Get("Authorization"))
					result := &http.Response{StatusCode: 200, Header: http.Header{}, Request: request}
					body := `{"id":"test-op","status":"Succeeded","result":{"contents":[` +
						`{"kind":"document","mimeType":"application/pdf","fields":` +
						`{"Big":{"valueInteger":9007199254740993,"confidence":0.7}}}],"warnings":[{"code":"Ignored"}]}}`
					if request.Method == "POST" {
						content, err := io.ReadAll(request.Body)
						require.NoError(t, err)
						assert.Equal(t, "wire sample", string(content))
						assert.Equal(t, "application/octet-stream", request.Header.Get("Content-Type"))
						result.StatusCode = 202
						result.Header.Set("Operation-Location",
							"https://resource.services.ai.azure.com/contentunderstanding/analyzerResults/test-op")
						body = `{"id":"test-op","status":"Running"}`
					}
					result.Body = io.NopCloser(strings.NewReader(body))
					return result, nil
				})})
			require.NoError(t, err)
			factory := func(*cobra.Command, *azdext.ExtensionContext, config.Options) (*serviceSession, error) {
				return &serviceSession{client: client, close: func() {}}, nil
			}
			root := newRootCommand(factory)
			var stdout bytes.Buffer
			root.SetOut(&stdout)
			root.SetErr(io.Discard)
			root.SetArgs([]string{"analyzer", "test", "invoice", "--file", sample, "--api-version", version})
			require.NoError(t, root.ExecuteContext(t.Context()))
			assert.Contains(t, stdout.String(), "9007199254740993")
			assert.NotContains(t, stdout.String(), "warnings")
			assert.Equal(t, []string{"POST /contentunderstanding/analyzers/invoice:analyzeBinary",
				"GET /contentunderstanding/analyzerResults/test-op"}, calls)
			assert.Zero(t, f.opens)
		})
	}
}
