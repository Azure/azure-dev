// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"testing"

	"azure.ai.content-understanding/internal/armclient"
	"azure.ai.content-understanding/internal/config"
	"azure.ai.content-understanding/internal/cuclient"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
)

type analyzerCopyGet struct {
	Name    string          `json:"name"`
	Result  json.RawMessage `json:"result"`
	Missing bool            `json:"missing"`
}

type analyzerCopyCase struct {
	Name      string                   `json:"name"`
	HTTP      []recordedInteraction    `json:"http"`
	Gets      []analyzerCopyGet        `json:"gets"`
	Copied    bool                     `json:"copied"`
	Result    json.RawMessage          `json:"result"`
	ErrorCode string                   `json:"error_code"`
	Error     *commandErrorExpectation `json:"error"`
}

type copyRequestObserver struct {
	cuclient.Client
	gets     []analyzerCopyGet
	requests []cuclient.CopyAnalyzerRequest
	results  []json.RawMessage
	grants   int
}

func (o *copyRequestObserver) GetAnalyzer(ctx context.Context, name string) (cuclient.Analyzer, error) {
	value, err := o.Client.GetAnalyzer(ctx, name)
	call := analyzerCopyGet{Name: name, Result: json.RawMessage("null")}
	if err == nil {
		call.Result, err = analyzerJSON(value)
	} else if domain, ok := errors.AsType[*cuclient.Error](err); ok {
		call.Missing = domain.Kind == cuclient.ErrorNotFound && domain.StatusCode == 404
	}
	o.gets = append(o.gets, call)
	return value, err
}

func (o *copyRequestObserver) CopyAnalyzer(
	ctx context.Context, request cuclient.CopyAnalyzerRequest,
) (cuclient.AnalyzerResponse, error) {
	o.requests = append(o.requests, request)
	response, err := o.Client.CopyAnalyzer(ctx, request)
	if err != nil {
		return response, err
	}
	raw, err := analyzerJSON(response.Analyzer)
	if err != nil {
		return cuclient.AnalyzerResponse{}, err
	}
	o.results = append(o.results, raw)
	return response, nil
}

func (o *copyRequestObserver) GrantCopyAuthorization(
	context.Context, cuclient.GrantCopyAuthorizationRequest,
) (cuclient.CopyAuthorization, error) {
	o.grants++
	return cuclient.CopyAuthorization{}, errors.New("Same-resource binding attempted a cross-resource copy grant.")
}

func TestAnalyzerCopyContracts(t *testing.T) {
	var fixture struct {
		Cases []analyzerCopyCase `json:"cases"`
	}
	loadCommandFixture(t, loadParityInventory(t).CoreCopyBinding, &fixture)
	for _, row := range fixture.Cases {
		for index, args := range [][]string{
			{"analyzer", "copy", "--source", "dev", "--destination", "prod"},
			{"analyzer", "copy", "dev", "prod"},
		} {
			for _, format := range []string{"default", "json", "none"} {
				t.Run(fmt.Sprintf("%s/%d/%s", row.Name, index, format), func(t *testing.T) {
					executeAnalyzerCopy(t, index == 0, row, args, format)
				})
			}
		}
	}
}

func executeAnalyzerCopy(
	t *testing.T, named bool, row analyzerCopyCase, args []string, format string,
) {
	t.Helper()
	_ = newDefaultsFixture(t)
	transport := &recordedTransport{fixture: recordedFixture{Interactions: row.HTTP}}
	credential := &recordedCredential{}
	observer := &copyRequestObserver{}
	opens, closes, prompts, discoveries, switches := 0, 0, 0, 0, 0
	var binding map[string]string
	root := newRootCommand(func(
		cmd *cobra.Command, _ *azdext.ExtensionContext, options config.Options,
	) (*serviceSession, error) {
		opens++
		binding = map[string]string{}
		if named {
			require.Empty(t, cmd.Flags().Args())
			for _, flag := range []string{"source", "destination"} {
				require.True(t, cmd.Flags().Changed(flag))
				value, err := cmd.Flags().GetString(flag)
				require.NoError(t, err)
				binding[flag] = value
			}
		} else {
			require.False(t, cmd.Flags().Changed("source"))
			require.False(t, cmd.Flags().Changed("destination"))
			require.Len(t, cmd.Flags().Args(), 2)
			binding["source"], binding["destination"] = cmd.Flags().Args()[0], cmd.Flags().Args()[1]
		}
		require.Equal(t, "https://sanitized.services.ai.azure.com", options.Endpoint)
		require.Equal(t, cuclient.DefaultAPIVersion, options.APIVersion)
		client, err := cuclient.NewRESTTransport(options.Endpoint, credential,
			&cuclient.TransportOptions{APIVersion: options.APIVersion, Transport: transport})
		if err != nil {
			return nil, err
		}
		observer.Client = client
		return &serviceSession{
			client: observer, apiVersion: options.APIVersion, close: func() { closes++ },
			confirm: func(context.Context, string) (bool, error) {
				prompts++
				return false, errors.New("Unexpected same-resource copy binding prompt.")
			},
			resolveAccount: func(context.Context, string, string, string) (armclient.Account, error) {
				discoveries++
				return armclient.Account{}, errors.New("Analyzer IDs were treated as resource selectors.")
			},
			clientForEndpoint: func(string) (cuclient.Client, error) {
				switches++
				return nil, errors.New("Same-resource copy switched clients.")
			},
		}, nil
	})
	var stdout, stderr bytes.Buffer
	root.SetOut(&stdout)
	root.SetErr(&stderr)
	root.SetArgs(append(slices.Clone(args), "-o", format, "--no-prompt",
		"--endpoint", "https://sanitized.services.ai.azure.com", "--api-version", cuclient.DefaultAPIVersion))
	runErr := root.ExecuteContext(t.Context())
	require.NoError(t, transport.complete())
	require.Empty(t, stderr.String())
	require.Equal(t, 1, opens)
	require.Equal(t, 1, closes)
	require.Equal(t, len(row.HTTP), credential.calls)
	require.Zero(t, prompts)
	require.Zero(t, discoveries)
	require.Zero(t, switches)
	require.Zero(t, observer.grants)
	require.Equal(t, map[string]string{"source": "dev", "destination": "prod"}, binding)
	assertJSONEqual(t, row.Gets, observer.gets)
	if row.Copied {
		require.NoError(t, runErr)
		require.Len(t, observer.requests, 1)
		require.Equal(t, cuclient.CopyAnalyzerRequest{
			SourceAnalyzerID: binding["source"], DestinationAnalyzerID: binding["destination"],
		}, observer.requests[0])
		require.Len(t, observer.results, 1)
		assertJSONEqual(t, row.Result, observer.results[0])
		if format == "none" {
			require.Empty(t, stdout.String())
		} else {
			assertJSONEqual(t, row.Result, json.RawMessage(stdout.Bytes()))
		}
	} else {
		require.Error(t, runErr)
		service, ok := errors.AsType[*azdext.ServiceError](runErr)
		require.True(t, ok)
		require.Equal(t, row.ErrorCode, service.ErrorCode)
		assertJSONEqual(t, row.Error, commandErrorExpectation{
			Category: "service", Message: service.Message, Hint: &service.Suggestion, StatusCode: &service.StatusCode,
		})
		require.Empty(t, observer.requests)
		require.Empty(t, observer.results)
		require.Empty(t, stdout.String())
	}
}
