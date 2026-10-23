// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"testing"

	"azure.ai.content-understanding/internal/config"
	"azure.ai.content-understanding/internal/cuclient"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
)

func TestAnalyzerShowContracts(t *testing.T) {
	var fixture struct {
		Response json.RawMessage `json:"response"`
	}
	loadCommandFixture(t, loadParityInventory(t).CoreShowBindings, &fixture)
	type showCase struct {
		name    string
		args    []string
		code    string
		message string
		hint    string
	}
	var tests []showCase
	for index, args := range analyzerNameForms("show", "invoice-v1") {
		tests = append(tests, showCase{name: fmt.Sprintf("name/%d", index), args: args})
	}
	for _, flag := range []string{"--name", "-n", "-a"} {
		tests = append(tests,
			showCase{name: "trimmed/" + flag, args: []string{"analyzer", "show", flag, " invoice-v1 "}},
			showCase{
				"conflict/" + flag, []string{"analyzer", "show", "invoice-v1", flag, "invoice-v2"},
				"conflicting_analyzer_name", "Specify the analyzer name only once.",
				"Use a positional name or --name, not both.",
			})
	}
	tests = append(tests, showCase{
		"missing", []string{"analyzer", "show"}, "missing_analyzer_name",
		"Specify an analyzer name.", "Use a positional name or --name.",
	})
	for _, test := range tests {
		for _, format := range []string{"default", "json", "none"} {
			t.Run(test.name+"/"+format, func(t *testing.T) {
				_ = newDefaultsFixture(t)
				var response recordedFixture
				if test.code == "" {
					response = analyzerHTTPFixture(analyzerHTTPResponse{
						http.MethodGet, "invoice-v1", http.StatusOK, fixture.Response,
					})
				}
				transport := &recordedTransport{fixture: response}
				credential := &recordedCredential{}
				opens, closes, prompts := 0, 0, 0
				root := newRootCommand(func(
					_ *cobra.Command, _ *azdext.ExtensionContext, options config.Options,
				) (*serviceSession, error) {
					opens++
					require.Equal(t, "https://sanitized.services.ai.azure.com", options.Endpoint)
					require.Equal(t, cuclient.DefaultAPIVersion, options.APIVersion)
					client, err := cuclient.NewRESTTransport(options.Endpoint, credential,
						&cuclient.TransportOptions{APIVersion: options.APIVersion, Transport: transport})
					if err != nil {
						return nil, err
					}
					return &serviceSession{
						client: client, apiVersion: options.APIVersion, close: func() { closes++ },
						confirm: func(context.Context, string) (bool, error) {
							prompts++
							return false, errors.New("Unexpected show prompt.")
						},
					}, nil
				})
				var stdout, stderr bytes.Buffer
				root.SetOut(&stdout)
				root.SetErr(&stderr)
				root.SetArgs(append(slices.Clone(test.args), "-o", format, "--no-prompt",
					"--endpoint", "https://sanitized.services.ai.azure.com", "--api-version", cuclient.DefaultAPIVersion))
				err := root.ExecuteContext(t.Context())
				require.Empty(t, stderr.String())
				require.Zero(t, prompts)
				require.NoError(t, transport.complete())
				require.Equal(t, len(response.Interactions), credential.calls)
				if test.code != "" {
					local, ok := errors.AsType[*azdext.LocalError](err)
					require.True(t, ok)
					require.Equal(t, test.code, local.Code)
					require.Equal(t, "validation", string(local.Category))
					require.Equal(t, test.message, local.Message)
					require.Equal(t, test.hint, local.Suggestion)
					require.Zero(t, opens)
					require.Zero(t, closes)
					require.Empty(t, stdout.String())
				} else {
					require.NoError(t, err)
					require.Equal(t, 1, opens)
					require.Equal(t, 1, closes)
					if format == "none" {
						require.Empty(t, stdout.String())
					} else {
						assertJSONEqual(t, fixture.Response, json.RawMessage(stdout.Bytes()))
					}
				}
			})
		}
	}
}
