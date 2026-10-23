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
	"slices"
	"testing"

	"azure.ai.content-understanding/internal/config"
	"azure.ai.content-understanding/internal/cuclient"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
)

type commandErrorExpectation struct {
	Category   string  `json:"category"`
	Message    string  `json:"message"`
	Hint       *string `json:"hint"`
	StatusCode *int    `json:"status_code"`
}

func loadCommandFixture(t *testing.T, asset parityAsset, target any) {
	t.Helper()
	require.Equal(t, "testdata/parity/"+filepath.Base(asset.File), asset.File)
	body, err := os.ReadFile(filepath.Join("../..", asset.File))
	require.NoError(t, err)
	require.Equal(t, asset.SHA256, sha256Text(body))
	require.NoError(t, recordingHygiene(body))
	var fixture struct {
		SchemaVersion int `json:"schema_version"`
		Source        struct {
			Repository  string            `json:"repository"`
			Commit      string            `json:"commit"`
			License     string            `json:"license"`
			Files       map[string]string `json:"files"`
			Adaptations []string          `json:"adaptations,omitempty"`
		} `json:"source"`
		Data json.RawMessage `json:"data"`
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	require.NoError(t, decoder.Decode(&fixture))
	require.ErrorIs(t, decoder.Decode(new(any)), io.EOF)
	require.Equal(t, 1, fixture.SchemaVersion)
	require.Equal(t, "microsoft-foundry/content-understanding-cli", fixture.Source.Repository)
	require.Equal(t, paritySourceCommit, fixture.Source.Commit)
	require.Equal(t, "MIT", fixture.Source.License)
	require.NotEmpty(t, fixture.Source.Files)
	for path, digest := range fixture.Source.Files {
		require.NotContains(t, path, "..")
		require.Regexp(t, "^[0-9a-f]{64}$", digest)
	}
	decoder = json.NewDecoder(bytes.NewReader(fixture.Data))
	decoder.DisallowUnknownFields()
	require.NoError(t, decoder.Decode(target))
	require.ErrorIs(t, decoder.Decode(new(any)), io.EOF)
}

func assertJSONEqual(t *testing.T, expected, actual any) {
	t.Helper()
	wanted, err := json.Marshal(expected)
	require.NoError(t, err)
	got, err := json.Marshal(actual)
	require.NoError(t, err)
	left, err := recordedJSON(wanted)
	require.NoError(t, err)
	right, err := recordedJSON(got)
	require.NoError(t, err)
	require.Equal(t, left, right)
}

func analyzerNameForms(action, name string) [][]string {
	return [][]string{
		{"analyzer", action, name},
		{"analyzer", action, "--name", name},
		{"analyzer", action, "-n", name},
		{"analyzer", action, "-a", name},
	}
}

type analyzerHTTPResponse struct {
	method string
	name   string
	status int
	body   json.RawMessage
}

func analyzerHTTPFixture(responses ...analyzerHTTPResponse) recordedFixture {
	var fixture recordedFixture
	for _, response := range responses {
		var step recordedInteraction
		step.Request.Method = response.method
		step.Request.URL = "https://sanitized.services.ai.azure.com/contentunderstanding/analyzers"
		if response.name != "" {
			step.Request.URL += "/" + response.name
		}
		step.Request.URL += "?api-version=" + cuclient.DefaultAPIVersion
		step.Request.Headers = map[string]string{"Accept": "application/json"}
		step.Request.BodyMode = "none"
		step.Request.Body = json.RawMessage("null")
		step.Response.Status = response.status
		step.Response.Headers = map[string]string{"Content-Type": "application/json"}
		step.Response.Body = response.body
		fixture.Interactions = append(fixture.Interactions, step)
	}
	return fixture
}

type analyzerCommandFixture struct {
	ListResponse      json.RawMessage `json:"list_response"`
	ListResult        json.RawMessage `json:"list_result"`
	DeleteAnalyzer    json.RawMessage `json:"delete_analyzer"`
	DeleteResult      json.RawMessage `json:"delete_result"`
	MissingListStatus json.RawMessage `json:"missing_list_status"`
	MissingGetStatus  json.RawMessage `json:"missing_get_status"`
}

func TestAnalyzerListAndDeleteContracts(t *testing.T) {
	var fixture analyzerCommandFixture
	loadCommandFixture(t, loadParityInventory(t).Normalized, &fixture)
	for _, test := range []struct {
		name     string
		forms    [][]string
		response recordedFixture
		output   json.RawMessage
	}{
		{"sorted-list", [][]string{
			{"analyzer", "list"},
			{"analyzer", "list", "--kind", "all", "--sort-by", "analyzerId"},
		}, analyzerHTTPFixture(analyzerHTTPResponse{
			http.MethodGet, "", http.StatusOK, fixture.ListResponse,
		}), fixture.ListResult},
		{"delete", analyzerNameForms("delete", "invoice-v1"), analyzerHTTPFixture(
			analyzerHTTPResponse{http.MethodGet, "invoice-v1", http.StatusOK, fixture.DeleteAnalyzer},
			analyzerHTTPResponse{http.MethodDelete, "invoice-v1", http.StatusNoContent, json.RawMessage("null")},
		), fixture.DeleteResult},
	} {
		for index, args := range test.forms {
			for _, format := range []string{"default", "json", "none"} {
				t.Run(fmt.Sprintf("%s/%d/%s", test.name, index, format), func(t *testing.T) {
					_ = newDefaultsFixture(t)
					transport := &recordedTransport{fixture: test.response}
					credential := &recordedCredential{}
					opens, closes, prompts := 0, 0, 0
					root := newRootCommand(func(
						cmd *cobra.Command, _ *azdext.ExtensionContext, options config.Options,
					) (*serviceSession, error) {
						opens++
						require.Equal(t, "https://sanitized.services.ai.azure.com", options.Endpoint)
						require.Equal(t, cuclient.DefaultAPIVersion, options.APIVersion)
						if cmd.Name() == "list" {
							kind, err := cmd.Flags().GetString("kind")
							require.NoError(t, err)
							require.Equal(t, "all", kind)
							sortBy, err := cmd.Flags().GetString("sort-by")
							require.NoError(t, err)
							require.Equal(t, "analyzerId", sortBy)
						} else {
							yes, err := cmd.Flags().GetBool("yes")
							require.NoError(t, err)
							require.True(t, yes)
						}
						client, err := cuclient.NewRESTTransport(options.Endpoint, credential,
							&cuclient.TransportOptions{APIVersion: options.APIVersion, Transport: transport})
						if err != nil {
							return nil, err
						}
						return &serviceSession{
							client: client, apiVersion: options.APIVersion, close: func() { closes++ },
							confirm: func(context.Context, string) (bool, error) {
								prompts++
								return false, errors.New("Unexpected command prompt.")
							},
						}, nil
					})
					var stdout, stderr bytes.Buffer
					root.SetOut(&stdout)
					root.SetErr(&stderr)
					invocation := slices.Clone(args)
					if test.name == "delete" {
						invocation = append(invocation, "--yes")
					}
					root.SetArgs(append(invocation, "-o", format, "--no-prompt",
						"--endpoint", "https://sanitized.services.ai.azure.com",
						"--api-version", cuclient.DefaultAPIVersion))
					require.NoError(t, root.ExecuteContext(t.Context()))
					require.Empty(t, stderr.String())
					require.Zero(t, prompts)
					require.Equal(t, 1, opens)
					require.Equal(t, 1, closes)
					require.NoError(t, transport.complete())
					require.Equal(t, len(test.response.Interactions), credential.calls)
					if format == "none" {
						require.Empty(t, stdout.String())
					} else {
						assertJSONEqual(t, test.output, json.RawMessage(stdout.Bytes()))
					}
				})
			}
		}
	}
}

func TestAnalyzerDeleteConflictingOperands(t *testing.T) {
	for _, flag := range []string{"--name", "-a"} {
		for _, format := range []string{"default", "json", "none"} {
			t.Run(flag+"/"+format, func(t *testing.T) {
				opens := 0
				root := newRootCommand(func(
					*cobra.Command, *azdext.ExtensionContext, config.Options,
				) (*serviceSession, error) {
					opens++
					return nil, errors.New("Conflicting names opened a service session.")
				})
				var stdout, stderr bytes.Buffer
				root.SetOut(&stdout)
				root.SetErr(&stderr)
				root.SetArgs([]string{"analyzer", "delete", "two", flag, "one", "--yes", "-o", format, "--no-prompt"})
				err := root.ExecuteContext(t.Context())
				local, ok := errors.AsType[*azdext.LocalError](err)
				require.True(t, ok)
				require.Equal(t, "validation", string(local.Category))
				require.Equal(t, "conflicting_analyzer_name", local.Code)
				require.Equal(t, "Specify the analyzer name only once.", local.Message)
				require.Equal(t, "Use a positional name or --name, not both.", local.Suggestion)
				require.Zero(t, opens)
				require.Empty(t, stdout.String())
				require.Empty(t, stderr.String())
			})
		}
	}
}

func TestAnalyzerResponsesRequireStatus(t *testing.T) {
	var fixture analyzerCommandFixture
	loadCommandFixture(t, loadParityInventory(t).Normalized, &fixture)
	for _, test := range []struct {
		name string
		args []string
		step analyzerHTTPResponse
	}{
		{"list", []string{"analyzer", "list"},
			analyzerHTTPResponse{http.MethodGet, "", http.StatusOK, fixture.MissingListStatus}},
		{"get-before-delete", []string{"analyzer", "delete", "invoice-v1", "--yes"},
			analyzerHTTPResponse{http.MethodGet, "invoice-v1", http.StatusOK, fixture.MissingGetStatus}},
	} {
		t.Run(test.name, func(t *testing.T) {
			transport := &recordedTransport{fixture: analyzerHTTPFixture(test.step)}
			root := newRecordedCommand(t, transport, "")
			root.SetArgs(append(slices.Clone(test.args), "-o", "json", "--no-prompt",
				"--endpoint", "https://sanitized.services.ai.azure.com", "--api-version", cuclient.DefaultAPIVersion))
			var stdout, stderr bytes.Buffer
			root.SetOut(&stdout)
			root.SetErr(&stderr)
			err := root.ExecuteContext(t.Context())
			service, ok := errors.AsType[*azdext.ServiceError](err)
			require.True(t, ok)
			require.Equal(t, "malformed_response", service.ErrorCode)
			require.Empty(t, stdout.String())
			require.Empty(t, stderr.String())
		})
	}
}
