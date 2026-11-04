// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"azure.ai.content-understanding/internal/config"
	"azure.ai.content-understanding/internal/cuclient"
	"azure.ai.content-understanding/internal/testutil"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestInlineAnalysisWithoutMIMETypeThroughCommand(t *testing.T) {
	const terminal = `{"status":"Succeeded","result":{"contents":[{"kind":"document","markdown":"# Invoice",` +
		`"startPageNumber":1,"endPageNumber":1}]},"usage":{"documentPagesStandardInline":1}}`
	for _, input := range []string{"file", "url"} {
		for _, view := range []string{"llm", "full", "usage"} {
			t.Run(input+"/"+view, func(t *testing.T) {
				_ = newAnalyzeFixture(t)
				path := filepath.Join(testutil.TempDir(t), "invoice.pdf")
				require.NoError(t, os.WriteFile(path, []byte(recordedBinaryInput), 0o600))
				requests := 0
				root := newRootCommand(func(
					_ *cobra.Command, _ *azdext.ExtensionContext, options config.Options,
				) (*serviceSession, error) {
					transport := copyWireTransport(func(request *http.Request) (*http.Response, error) {
						requests++
						assert.Equal(t, http.MethodPost, request.Method)
						assert.Equal(t, cuclient.PreviewAPIVersion, request.URL.Query().Get("api-version"))
						body, err := io.ReadAll(request.Body)
						require.NoError(t, err)
						if input == "file" {
							assert.Equal(t, "/contentunderstanding/analyzers/prebuilt-layout:analyzeBinaryInline",
								request.URL.Path)
							assert.Equal(t, recordedBinaryInput, string(body))
						} else {
							assert.Equal(t, "/contentunderstanding/analyzers/prebuilt-layout:analyzeInline",
								request.URL.Path)
							assert.JSONEq(t, `{"inputs":[{"url":"https://example.com/invoice.pdf"}]}`, string(body))
						}
						return &http.Response{StatusCode: http.StatusOK, Request: request, Header: http.Header{},
							Body: io.NopCloser(strings.NewReader(terminal))}, nil
					})
					client, err := cuclient.NewRESTTransport(options.Endpoint, &copyWireCredential{},
						&cuclient.TransportOptions{APIVersion: options.APIVersion, Transport: transport})
					if err != nil {
						return nil, err
					}
					return &serviceSession{client: client, apiVersion: options.APIVersion,
						analyzer: "prebuilt-layout", close: func() {}}, nil
				})
				report := filepath.Join(testutil.TempDir(t), "inline-report.json")
				args := []string{"analyze", "--inline", "--api-version", cuclient.PreviewAPIVersion,
					"--endpoint", "https://resource.services.ai.azure.com", "--analyzer", "prebuilt-layout",
					"--report-file", report}
				if input == "file" {
					args = append(args, "--file", path)
				} else {
					args = append(args, "--url", "https://example.com/invoice.pdf")
				}
				if view == "full" {
					args = append(args, "--output", "json")
				} else if view == "usage" {
					args = append(args, "--usage")
				}
				root.SetArgs(args)
				var stdout, stderr bytes.Buffer
				root.SetOut(&stdout)
				root.SetErr(&stderr)
				require.NoError(t, root.ExecuteContext(t.Context()))
				assert.Equal(t, 1, requests, "inline success must not poll or fall back to async")
				assert.Empty(t, stderr.String())
				switch view {
				case "llm":
					assert.Contains(t, stdout.String(), "mimeType: unknown\n")
					assert.Contains(t, stdout.String(), "# Invoice")
				case "full":
					assert.JSONEq(t, terminal, stdout.String())
					assert.NotContains(t, stdout.String(), "mimeType")
				case "usage":
					var result struct {
						Result string         `json:"result"`
						Usage  map[string]int `json:"usage"`
					}
					require.NoError(t, json.Unmarshal(stdout.Bytes(), &result))
					assert.Contains(t, result.Result, "# Invoice")
					assert.Equal(t, 1, result.Usage["documentPagesStandardInline"])
				}
				// #nosec G304 -- Reads this test's explicitly requested private report.
				body, err := os.ReadFile(report)
				require.NoError(t, err)
				var result struct {
					Counts struct {
						Succeeded int `json:"succeeded"`
						Failed    int `json:"failed"`
					} `json:"counts"`
				}
				require.NoError(t, json.Unmarshal(body, &result))
				assert.Equal(t, 1, result.Counts.Succeeded)
				assert.Zero(t, result.Counts.Failed)
			})
		}
	}
}
