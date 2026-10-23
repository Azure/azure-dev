// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"maps"
	"testing"

	"azure.ai.content-understanding/internal/config"
	"azure.ai.content-understanding/internal/cuclient"
	"azure.ai.content-understanding/internal/defaults"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type defaultsFake struct {
	cuclient.Client
	current   cuclient.Defaults
	getErr    error
	updateErr error
	calls     []string
	updates   []cuclient.UpdateDefaultsRequest
	onGet     func()
}

func (f *defaultsFake) GetDefaults(context.Context) (cuclient.Defaults, error) {
	f.calls = append(f.calls, "get")
	if f.onGet != nil {
		f.onGet()
	}
	return f.current, f.getErr
}

func (f *defaultsFake) UpdateDefaults(
	_ context.Context, request cuclient.UpdateDefaultsRequest,
) (cuclient.Defaults, error) {
	f.calls = append(f.calls, "update")
	f.updates = append(f.updates, request)
	// Model the actual merge-patch service: omitted existing keys are not deleted.
	mapping := defaults.Merge(f.current.ModelDeployments, request.ModelDeployments)
	return cuclient.Defaults{ModelDeployments: mapping}, f.updateErr
}

type defaultsFixture struct {
	client        *defaultsFake
	models        map[string]string
	environment   string
	analyzer      string
	endpoint      string
	options       config.Options
	openErr       error
	opens, closes int
	onOpen        func()
	sources       map[string]string
}

func newDefaultsFixture(t *testing.T) *defaultsFixture {
	t.Helper()
	for _, key := range []string{"AZD_DEBUG", "AZD_NO_PROMPT", "AZD_ENVIRONMENT", "AZD_CWD"} {
		t.Setenv(key, "")
	}
	mapping := defaults.WithAliases(map[string]string{
		"gpt-4.1": "old-completion", "text-embedding-3-large": "embedding",
	})
	return &defaultsFixture{
		client: &defaultsFake{current: cuclient.Defaults{ModelDeployments: mapping}},
		models: map[string]string{"gpt-5.2": "environment-deployment"}, environment: "dev",
		endpoint: "https://configured.services.ai.azure.com/contentunderstanding",
	}
}

func (f *defaultsFixture) root() *cobra.Command {
	return newRootCommand(func(
		_ *cobra.Command, extCtx *azdext.ExtensionContext, options config.Options,
	) (*serviceSession, error) {
		f.opens++
		f.options = options
		if f.openErr != nil {
			return nil, f.openErr
		}
		if f.onOpen != nil {
			f.onOpen()
		}
		environment := f.environment
		if extCtx.Environment != "" {
			environment = extCtx.Environment
		}
		version := options.APIVersion
		if version == "" {
			version = cuclient.DefaultAPIVersion
		}
		return &serviceSession{
			client: f.client, models: f.models, apiVersion: version, environment: environment,
			endpoint: f.endpoint, analyzer: f.analyzer,
			sources: f.sources,
			close:   func() { f.closes++ },
			confirm: func(context.Context, string) (bool, error) {
				return false, errors.New("defaults and doctor must never invoke an implicit prompt")
			},
		}, nil
	})
}

func (f *defaultsFixture) execute(ctx context.Context, args ...string) (string, string, error) {
	root := f.root()
	var stdout, stderr bytes.Buffer
	root.SetOut(&stdout)
	root.SetErr(&stderr)
	root.SetArgs(args)
	err := root.ExecuteContext(ctx)
	return stdout.String(), stderr.String(), err
}

func TestDefaultsShowPreservesCompleteResponseAndFormats(t *testing.T) {
	for _, format := range []string{"default", "json", "none"} {
		t.Run(format, func(t *testing.T) {
			f := newDefaultsFixture(t)
			raw := `{"modelDeployments":{"gpt-4.1":"deployment"},"future":{"number":9007199254740993}}`
			f.client.current = cuclient.Defaults{
				ModelDeployments: map[string]string{"gpt-4.1": "deployment"}, JSON: json.RawMessage(raw),
			}
			stdout, stderr, err := f.execute(t.Context(), "defaults", "show", "-o", format,
				"--endpoint", "https://explicit.services.ai.azure.com", "--api-version", cuclient.PreviewAPIVersion)
			require.NoError(t, err)
			assert.Empty(t, stderr)
			if format == "none" {
				assert.Empty(t, stdout)
			} else {
				assert.JSONEq(t, raw, stdout)
				assert.Contains(t, stdout, "9007199254740993")
			}
			assert.Equal(t, []string{"get"}, f.client.calls)
			assert.Equal(t, f.opens, f.closes)
			assert.Equal(t, "https://explicit.services.ai.azure.com", f.options.Endpoint)
			assert.Equal(t, cuclient.PreviewAPIVersion, f.options.APIVersion)
		})
	}
}

func TestDefaultsSetExplicitEnvironmentAndServicePrecedence(t *testing.T) {
	for _, fromEnvironment := range []bool{false, true} {
		f := newDefaultsFixture(t)
		before := maps.Clone(f.models)
		args := []string{"defaults", "set", "--model", "gpt-5.2=explicit", "--model", "other=custom", "--no-prompt"}
		if fromEnvironment {
			args = append(args, "--from-environment")
		}
		stdout, stderr, err := f.execute(t.Context(), args...)
		require.NoError(t, err)
		assert.Empty(t, stderr)
		assert.Equal(t, []string{"get", "update"}, f.client.calls)
		require.Len(t, f.client.updates, 1)
		models := f.client.updates[0].ModelDeployments
		assert.Equal(t, "explicit", models["gpt-5.2"])
		assert.Equal(t, "explicit", models[defaults.Completion])
		assert.Equal(t, "old-completion", models["gpt-4.1"])
		assert.Equal(t, "embedding", models[defaults.Embedding])
		assert.Contains(t, stdout, `"modelDeployments"`)
		assert.Equal(t, before, f.models)
		assert.Equal(t, 1, f.closes)
	}
	f := newDefaultsFixture(t)
	stdout, _, err := f.execute(t.Context(), "defaults", "set", "--from-environment", "-e", "selected")
	require.NoError(t, err)
	assert.Contains(t, stdout, "environment-deployment")
	assert.Equal(t, "environment-deployment", f.client.updates[0].ModelDeployments[defaults.Completion])
}

func TestDefaultsReplaceSkipsClientMergeButDoesNotPromiseServerDeletion(t *testing.T) {
	f := newDefaultsFixture(t)
	stdout, stderr, err := f.execute(t.Context(),
		"defaults", "set", "--model", "gpt-5.2=new", "--replace", "-o", "json")
	require.NoError(t, err)
	assert.Empty(t, stderr)
	assert.Equal(t, []string{"update"}, f.client.calls)
	require.Len(t, f.client.updates, 1)
	assert.NotContains(t, f.client.updates[0].ModelDeployments, "gpt-4.1")
	assert.Contains(t, stdout, `"gpt-4.1": "old-completion"`, "service returned mappings remain authoritative")
}

func TestDefaultsRejectUnsupportedLocalKeysProfilesAndMalformedModels(t *testing.T) {
	for _, args := range [][]string{
		{"defaults", "set"},
		{"defaults", "set", "--model", "private"},
		{"defaults", "set", "--model", "=private"},
		{"defaults", "set", "--model", "model="},
		{"defaults", "set", "--key", "cu.apiKey", "--value", "private"},
		{"defaults", "set", "--from-profile"},
		{"defaults", "set", "--api-key", "private"},
		{"defaults", "show", "--profile", "private"},
		{"defaults", "show", "--table"},
		{"defaults", "show", "--key", "cu.defaultAnalyzer"},
		{"defaults", "show", "extra"},
		{"defaults", "set", "--json"},
		{"defaults", "show", "-o", "table"},
	} {
		f := newDefaultsFixture(t)
		stdout, stderr, err := f.execute(t.Context(), args...)
		require.Error(t, err)
		assert.NotContains(t, err.Error(), "private")
		assert.Empty(t, stdout)
		assert.Empty(t, stderr)
		assert.Zero(t, f.opens)
		assert.Empty(t, f.client.calls)
	}
}

func TestDefaultsAbsentModelsAndServiceFailuresDoNotWrite(t *testing.T) {
	for _, test := range []struct {
		name string
		err  error
	}{
		{"auth", &cuclient.Error{
			Kind: cuclient.ErrorAuthentication, Code: "Unauthorized", Message: "Authentication failed.",
		}},
		{"missing resource", &cuclient.Error{
			Kind: cuclient.ErrorNotFound, Code: "ResourceNotFound", Message: "Resource missing.",
		}},
		{"timeout", context.DeadlineExceeded},
		{"opaque failure", errors.New("private token=private")},
	} {
		t.Run(test.name, func(t *testing.T) {
			f := newDefaultsFixture(t)
			f.client.getErr = test.err
			stdout, stderr, err := f.execute(t.Context(), "defaults", "set", "--model", "model=deployment")
			require.Error(t, err)
			assert.Empty(t, stdout)
			assert.Empty(t, stderr)
			assert.NotContains(t, err.Error(), "private")
			assert.Empty(t, f.client.updates)
			assert.Equal(t, 1, f.closes)
		})
	}
	f := newDefaultsFixture(t)
	f.models, f.environment = nil, ""
	stdout, _, err := f.execute(t.Context(), "defaults", "set", "--from-environment")
	require.Error(t, err)
	assert.Empty(t, stdout)
	assert.Empty(t, f.client.calls)
	assert.Equal(t, 1, f.closes)
	f = newDefaultsFixture(t)
	f.client.getErr = &cuclient.Error{
		Kind: cuclient.ErrorNotFound, Code: "DefaultsNotSet", Message: "Defaults not set.", StatusCode: 404,
	}
	stdout, _, err = f.execute(t.Context(), "defaults", "show")
	require.ErrorContains(t, err, "have not been configured")
	assert.Empty(t, stdout)
	stdout, _, err = f.execute(t.Context(), "defaults", "set", "--model", "gpt-5.2=deployment", "-o", "none")
	require.NoError(t, err)
	assert.Empty(t, stdout)
	assert.Len(t, f.client.updates, 1)
}

func TestDefaultsCancellationAndSessionFailures(t *testing.T) {
	for _, mode := range []string{"before open", "during open", "during get", "open failure", "update failure"} {
		t.Run(mode, func(t *testing.T) {
			f := newDefaultsFixture(t)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			switch mode {
			case "before open":
				cancel()
			case "during open":
				f.onOpen = cancel
			case "during get":
				f.client.onGet = cancel
			case "open failure":
				f.openErr = errors.New("private host detail")
			case "update failure":
				f.client.updateErr = context.DeadlineExceeded
			}
			stdout, stderr, err := f.execute(ctx, "defaults", "set", "--from-environment")
			require.Error(t, err)
			assert.Empty(t, stdout)
			assert.Empty(t, stderr)
			assert.NotContains(t, err.Error(), "private")
			if mode != "update failure" {
				assert.Empty(t, f.client.updates)
			}
		})
	}
}
