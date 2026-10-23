// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"azure.ai.content-understanding/internal/armclient"
	"azure.ai.content-understanding/internal/config"
	"azure.ai.content-understanding/internal/cuclient"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type commandCopyFake struct {
	analyzerFake
	exists  bool
	grant   cuclient.GrantCopyAuthorizationRequest
	copy    cuclient.CopyAnalyzerRequest
	copyErr error
}

func (f *commandCopyFake) GetAnalyzer(ctx context.Context, id string) (cuclient.Analyzer, error) {
	if err := f.record(ctx, "get:"+id); err != nil {
		return cuclient.Analyzer{}, err
	}
	if id == "destination" && !f.exists {
		return cuclient.Analyzer{}, &cuclient.Error{Kind: cuclient.ErrorNotFound, StatusCode: 404}
	}
	return analyzerWithJSON(id, `{"analyzerId":"`+id+`"}`), nil
}

func (f *commandCopyFake) GrantCopyAuthorization(
	ctx context.Context, request cuclient.GrantCopyAuthorizationRequest,
) (cuclient.CopyAuthorization, error) {
	f.grant = request
	return cuclient.CopyAuthorization{Source: "never-print-grant-secret"}, f.record(ctx, "grant")
}

func (f *commandCopyFake) CopyAnalyzer(
	ctx context.Context, request cuclient.CopyAnalyzerRequest,
) (cuclient.AnalyzerResponse, error) {
	f.copy = request
	if err := f.record(ctx, "copy"); err != nil {
		return cuclient.AnalyzerResponse{}, err
	}
	return cuclient.AnalyzerResponse{Analyzer: f.analyzer}, f.copyErr
}

type commandCopyFixture struct {
	source, destination               *commandCopyFake
	opens, closes                     int
	resolutions                       [][3]string
	endpoints                         []string
	options                           config.Options
	sourceAccount, destinationAccount armclient.Account
	discoveryErr, clientErr, openErr  error
}

func newCommandCopyFixture(t *testing.T) *commandCopyFixture {
	t.Helper()
	_ = newAnalyzerFixture(t)
	fake := func() *commandCopyFake {
		return &commandCopyFake{analyzerFake: analyzerFake{analyzer: analyzerWithJSON("destination",
			`{"analyzerId":"destination","warnings":[{"code":"Notice"}],"unknown":9007199254740993}`)}}
	}
	return &commandCopyFixture{
		source: fake(), destination: fake(),
		sourceAccount: armclient.Account{
			ID: "/source/account", Endpoint: "https://source.services.ai.azure.com/contentunderstanding", Region: "westus"},
		destinationAccount: armclient.Account{
			ID: "/destination/account", Endpoint: "https://destination.services.ai.azure.com/contentunderstanding",
			Region: "eastus"},
	}
}

func (f *commandCopyFixture) execute(ctx context.Context, args ...string) (string, error) {
	root := newRootCommand(func(
		_ *cobra.Command, _ *azdext.ExtensionContext, options config.Options,
	) (*serviceSession, error) {
		f.opens++
		f.options = options
		if f.openErr != nil {
			return nil, f.openErr
		}
		return &serviceSession{
			client: f.source, endpoint: "https://configured.services.ai.azure.com/contentunderstanding",
			close: func() { f.closes++ },
			resolveAccount: func(_ context.Context, resource, subscription, group string) (armclient.Account, error) {
				f.resolutions = append(f.resolutions, [3]string{resource, subscription, group})
				if f.discoveryErr != nil {
					return armclient.Account{}, f.discoveryErr
				}
				if strings.Contains(resource, "destination") {
					return f.destinationAccount, nil
				}
				return f.sourceAccount, nil
			},
			clientForEndpoint: func(endpoint string) (cuclient.Client, error) {
				f.endpoints = append(f.endpoints, endpoint)
				if f.clientErr != nil {
					return nil, f.clientErr
				}
				if endpoint == f.sourceAccount.Endpoint {
					return f.source, nil
				}
				return f.destination, nil
			},
		}, nil
	})
	var stdout, stderr bytes.Buffer
	root.SetOut(&stdout)
	root.SetErr(&stderr)
	root.SetArgs(append([]string{"analyzer", "copy"}, args...))
	err := root.ExecuteContext(ctx)
	return stdout.String(), err
}

func TestAnalyzerCopySameResourceOutputAndFlags(t *testing.T) {
	for _, args := range [][]string{
		{"source", "destination"},
		{"--source", "source", "--destination", "destination", "-o", "json"},
		{"source", "--destination", "destination", "--endpoint", "https://explicit.services.ai.azure.com",
			"--api-version", cuclient.PreviewAPIVersion},
		{"source", "destination", "-o", "none"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			f := newCommandCopyFixture(t)
			stdout, err := f.execute(t.Context(), args...)
			require.NoError(t, err)
			assert.Equal(t, 1, f.opens)
			assert.Equal(t, 1, f.closes)
			assert.Empty(t, f.resolutions)
			assert.Empty(t, f.endpoints)
			assert.Equal(t, []string{"get:source", "get:destination", "copy"}, f.source.calls)
			assert.Empty(t, f.source.grant.AnalyzerID)
			assert.Empty(t, f.source.copy.SourceAzureResourceID)
			assert.NotContains(t, stdout, "secret")
			if args[len(args)-1] == "none" {
				assert.Empty(t, stdout)
			} else {
				assert.JSONEq(t, string(f.source.analyzer.JSON), stdout)
				assert.Contains(t, stdout, "9007199254740993")
			}
			if strings.Contains(strings.Join(args, " "), "--endpoint") {
				assert.Equal(t, "https://explicit.services.ai.azure.com", f.options.Endpoint)
				assert.Equal(t, cuclient.PreviewAPIVersion, f.options.APIVersion)
			}
		})
	}
}

func TestAnalyzerCopyCrossResourceAndOmittedDestination(t *testing.T) {
	for _, mode := range []string{
		"cross", "omitted destination", "same ARM account", "source endpoint fallback", "same ID across resources",
	} {
		t.Run(mode, func(t *testing.T) {
			f := newCommandCopyFixture(t)
			args := []string{"source", "destination", "--source-resource", "source-account",
				"--source-subscription", "source-sub", "--source-resource-group", "source-group"}
			switch mode {
			case "same ARM account":
				f.destinationAccount.ID = strings.ToUpper(f.sourceAccount.ID)
				args = append(args, "--destination-resource", "destination-account")
			case "source endpoint fallback":
				args = []string{"source", "destination", "--destination-resource", "destination-account"}
			case "same ID across resources":
				args[1] = "source"
				f.destination.err = &cuclient.Error{Kind: cuclient.ErrorNotFound, StatusCode: 404}
				f.destination.onCall = func(_ context.Context, call string) {
					if call == "copy" {
						f.destination.err = nil
					}
				}
				args = append(args, "--destination-resource", "destination-account")
			case "cross":
				args = append(args, "--destination-resource", "destination-account",
					"--destination-subscription", "destination-sub", "--destination-resource-group", "destination-group")
			}
			stdout, err := f.execute(t.Context(), args...)
			require.NoError(t, err)
			assert.NotEmpty(t, stdout)
			assert.Equal(t, 1, f.closes)
			if mode == "omitted destination" || mode == "same ARM account" {
				assert.Empty(t, f.source.grant.AnalyzerID)
				assert.Len(t, f.endpoints, 1)
				assert.Empty(t, f.destination.calls)
			} else {
				assert.Len(t, f.endpoints, 2)
				assert.Equal(t, f.destinationAccount.ID, f.source.grant.TargetAzureResourceID)
				assert.Equal(t, f.sourceAccount.ID, f.destination.copy.SourceAzureResourceID)
				assert.Equal(t, "westus", f.destination.copy.SourceRegion)
			}
			if mode == "source endpoint fallback" {
				assert.Equal(t, "https://configured.services.ai.azure.com/contentunderstanding", f.resolutions[0][0])
			}
			if mode == "cross" {
				assert.Equal(t, [][3]string{
					{"source-account", "source-sub", "source-group"},
					{"destination-account", "destination-sub", "destination-group"},
				}, f.resolutions)
			}
		})
	}
}

func TestAnalyzerCopyInvalidInputsFailBeforeHost(t *testing.T) {
	for _, args := range [][]string{
		{}, {"source"}, {"source", "source"}, {"source", "destination", "extra"},
		{"source", "destination", "--source", "source"}, {"source", "destination", "--destination", "destination"},
		{"bad/id", "destination"}, {"source", "bad/id"},
		{"source", "destination", "--source-resource-group", "group"},
		{"source", "destination", "--source-resource", ""},
		{"source", "destination", "--source-subscription", ""},
		{"source", "destination", "--destination-resource", ""},
		{"source", "destination", "--destination-resource-group", "group"},
		{"source", "destination", "--destination-subscription", "sub"},
		{"source", "destination", "--source-resource", "http://unsafe.example"},
		{"source", "destination", "--source-resource",
			"/subscriptions/s/resourceGroups/g/providers/Microsoft.CognitiveServices/accounts/a",
			"--source-subscription", "conflict"},
		{"source", "destination", "--force"}, {"source", "destination", "--source-profile", "private"},
		{"source", "destination", "--api-key", "private-secret"}, {"source", "destination", "-o", "table"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			f := newCommandCopyFixture(t)
			stdout, err := f.execute(t.Context(), args...)
			require.Error(t, err)
			assert.Empty(t, stdout)
			assert.Zero(t, f.opens)
		})
	}
}

func TestAnalyzerCopyFailuresCloseSessionAndSuppressOutput(t *testing.T) {
	for _, mode := range []string{
		"open", "discovery", "client", "source", "existing", "copy", "invalid result", "cancelled",
	} {
		t.Run(mode, func(t *testing.T) {
			f := newCommandCopyFixture(t)
			args := []string{"source", "destination"}
			safeError := &cuclient.Error{Kind: cuclient.ErrorService, Code: "test_failure", Message: "safe failure"}
			ctx := t.Context()
			switch mode {
			case "open":
				f.openErr = safeError
			case "discovery":
				f.discoveryErr = safeError
			case "client":
				f.clientErr = safeError
			case "source":
				f.source.err = safeError
			case "existing":
				f.source.exists = true
			case "copy":
				f.source.copyErr = safeError
			case "invalid result":
				f.source.analyzer.JSON = nil
			case "cancelled":
				cancelled, cancel := context.WithCancel(ctx)
				cancel()
				ctx = cancelled
			}
			if mode == "discovery" || mode == "client" {
				args = append(args, "--source-resource", "source-account")
			}
			stdout, err := f.execute(ctx, args...)
			require.Error(t, err)
			assert.Empty(t, stdout)
			if mode == "cancelled" {
				assert.Zero(t, f.opens)
			} else if mode != "open" {
				assert.Equal(t, 1, f.closes)
			}
			assert.NotContains(t, err.Error(), "secret")
		})
	}
}

func TestAnalyzerCopySameResolvedAccountRejectsIdenticalID(t *testing.T) {
	f := newCommandCopyFixture(t)
	_, err := f.execute(t.Context(), "source", "source", "--source-resource", "source-account")
	require.Error(t, err)
	assert.Empty(t, f.source.calls)
	assert.Equal(t, 1, f.closes)
}

func TestAnalyzerCopyUnknownErrorRedaction(t *testing.T) {
	f := newCommandCopyFixture(t)
	f.source.copyErr = errors.New("private transport secret")
	stdout, err := f.execute(t.Context(), "source", "destination")
	require.Error(t, err)
	assert.Empty(t, stdout)
	assert.NotContains(t, err.Error(), "secret")
}
