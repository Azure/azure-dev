// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"azure.ai.content-understanding/internal/config"
	"azure.ai.content-understanding/internal/cuclient"
	"azure.ai.content-understanding/internal/exterrors"
	"azure.ai.content-understanding/internal/testutil"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/extensions"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type analyzerFake struct {
	cuclient.Client
	calls    []string
	list     []cuclient.Analyzer
	analyzer cuclient.Analyzer
	err      error
	create   cuclient.CreateAnalyzerRequest
	onCall   func(context.Context, string)
}

func (f *analyzerFake) record(ctx context.Context, call string) error {
	f.calls = append(f.calls, call)
	if f.onCall != nil {
		f.onCall(ctx, call)
	}
	return f.err
}

func (f *analyzerFake) ListAnalyzers(ctx context.Context) ([]cuclient.Analyzer, error) {
	return f.list, f.record(ctx, "list")
}

func (f *analyzerFake) GetAnalyzer(ctx context.Context, name string) (cuclient.Analyzer, error) {
	return f.analyzer, f.record(ctx, "get:"+name)
}

func (f *analyzerFake) CreateAnalyzer(
	ctx context.Context, request cuclient.CreateAnalyzerRequest,
) (cuclient.AnalyzerResponse, error) {
	f.create = request
	return cuclient.AnalyzerResponse{Analyzer: f.analyzer}, f.record(ctx, "create:"+request.AnalyzerID)
}

func (f *analyzerFake) DeleteAnalyzer(ctx context.Context, name string) error {
	return f.record(ctx, "delete:"+name)
}

type analyzerFixture struct {
	client        *analyzerFake
	opens, closes int
	options       config.Options
	version       string
	confirmed     bool
	confirmations []string
	openErr       error
	promptErr     error
	onPrompt      func()
}

func analyzerWithJSON(id, raw string) cuclient.Analyzer {
	return cuclient.Analyzer{ID: id, JSON: json.RawMessage(raw)}
}

func newAnalyzerFixture(t *testing.T) *analyzerFixture {
	t.Helper()
	for _, name := range []string{"AZD_DEBUG", "AZD_NO_PROMPT", "AZD_ENVIRONMENT", "AZD_CWD"} {
		t.Setenv(name, "")
	}
	alpha := analyzerWithJSON("alpha",
		`{"analyzerId":"alpha","description":"Alpha","extra":{"count":9007199254740993}}`)
	prebuilt := analyzerWithJSON("prebuilt-document", `{"analyzerId":"prebuilt-document","description":"Document"}`)
	return &analyzerFixture{
		client:  &analyzerFake{list: []cuclient.Analyzer{prebuilt, alpha}, analyzer: alpha},
		version: cuclient.DefaultAPIVersion, confirmed: true,
	}
}

func (f *analyzerFixture) root() *cobra.Command {
	return newRootCommand(func(
		_ *cobra.Command, _ *azdext.ExtensionContext, options config.Options,
	) (*serviceSession, error) {
		f.opens++
		f.options = options
		if f.openErr != nil {
			return nil, f.openErr
		}
		return &serviceSession{
			client: f.client, apiVersion: f.version,
			close: func() { f.closes++ },
			confirm: func(ctx context.Context, message string) (bool, error) {
				f.confirmations = append(f.confirmations, message)
				if f.onPrompt != nil {
					f.onPrompt()
				}
				return f.confirmed, f.promptErr
			},
		}, nil
	})
}

func (f *analyzerFixture) execute(ctx context.Context, args ...string) (string, string, error) {
	var stdout, stderr bytes.Buffer
	root := f.root()
	root.SetOut(&stdout)
	root.SetErr(&stderr)
	root.SetArgs(append([]string{"analyzer"}, args...))
	err := root.ExecuteContext(ctx)
	return stdout.String(), stderr.String(), err
}

func schemaFile(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(testutil.TempDir(t), "schema file.json")
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
	return path
}

func TestAnalyzerCommandGoldens(t *testing.T) {
	for _, action := range []string{"list", "show", "create", "delete"} {
		for _, format := range []string{"default", "json", "none"} {
			t.Run(action+"/"+format, func(t *testing.T) {
				f := newAnalyzerFixture(t)
				args := []string{action, "-o", format}
				if action != "list" {
					args = append(args, "alpha")
				}
				if action == "create" {
					args = append(args, "-s", schemaFile(t, `{"baseAnalyzerId":"prebuilt-document"}`))
				}
				if action == "delete" {
					args = append(args, "-y")
				}
				stdout, stderr, err := f.execute(t.Context(), args...)
				require.NoError(t, err)
				assert.Empty(t, stderr)
				assert.Equal(t, 1, f.opens)
				assert.Equal(t, 1, f.closes)
				assert.Empty(t, f.confirmations)
				if format == "none" {
					assert.Empty(t, stdout)
				} else {
					//nolint:gosec // Golden names come from the fixed command list above.
					expected, err := os.ReadFile("testdata/analyzer-" + action + ".json")
					require.NoError(t, err)
					assert.Equal(t, string(expected), stdout)
				}
			})
		}
	}
}

func TestAnalyzerNameAliases(t *testing.T) {
	for _, action := range []string{"show", "create", "delete"} {
		for _, names := range [][]string{
			{"invoice_v1"}, {"--name", "invoice_v1"}, {"-n", "invoice_v1"}, {"-a", "invoice_v1"},
			{"--name", "old", "-a", "invoice_v1"}, {" invoice_v1 "},
		} {
			t.Run(action+"/"+strings.Join(names, " "), func(t *testing.T) {
				f := newAnalyzerFixture(t)
				args := append([]string{action}, names...)
				if action == "create" {
					args = append(args, "--schema", schemaFile(t, `{}`))
				}
				if action == "delete" {
					args = append(args, "--yes")
				}
				_, stderr, err := f.execute(t.Context(), args...)
				require.NoError(t, err)
				assert.Empty(t, stderr)
				want := "get:invoice_v1"
				if action == "create" {
					want = "create:invoice_v1"
				}
				assert.Equal(t, want, f.client.calls[0])
			})
		}
	}
}

func TestAnalyzerParserFailuresAvoidRuntime(t *testing.T) {
	for _, args := range [][]string{
		{"show"}, {"show", "--name", ""}, {"show", " "}, {"show", "a/b"},
		{"show", "a", "b"}, {"show", "alpha", "--name", "alpha"},
		{"show", "alpha", "-n", "other"}, {"show", "alpha", "-a", "other"},
		{"create"}, {"create", "invoice"}, {"create", "invoice", "-s", " "},
		{"create", "invoice-v1", "-s", "unused"}, {"create", "invoice.v1", "-s", "unused"},
		{"create", "invoice", "-n", "invoice", "-s", "unused"}, {"create", "a", "b"},
		{"delete"}, {"delete", "a", "-a", "a", "--yes"},
		{"list", "extra"}, {"list", "--kind", "invalid"}, {"list", "--sort-by", "invalid"},
		{"list", "--kind", ""}, {"list", "--sort-by", ""},
		{"list", "--json"}, {"list", "--api-key", "private-key"},
		{"list", "-o", "table"}, {"list", "--kind"}, {"delete", "a", "--yes=invalid"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			f := newAnalyzerFixture(t)
			stdout, stderr, err := f.execute(t.Context(), args...)
			require.Error(t, err)
			assert.Empty(t, stdout)
			assert.Empty(t, stderr)
			assert.Zero(t, f.opens)
			assert.Empty(t, f.client.calls)
			assert.NotContains(t, err.Error(), "private-key")
		})
	}
}

func TestAnalyzerListFilteringAndSorting(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for _, tt := range []struct {
		kind, sort string
		want       []string
	}{
		{"all", "analyzerId", []string{"alpha", "prebuilt-document", "zeta"}},
		{"custom", "analyzerId", []string{"alpha", "zeta"}},
		{"prebuilt", "analyzerId", []string{"prebuilt-document"}},
		{"all", "createdAt", []string{"zeta", "alpha", "prebuilt-document"}},
		{"all", "lastModifiedAt", []string{"prebuilt-document", "alpha", "zeta"}},
	} {
		t.Run(tt.kind+"/"+tt.sort, func(t *testing.T) {
			f := newAnalyzerFixture(t)
			alpha := f.client.analyzer
			alpha.CreatedAt, alpha.LastModifiedAt = now, now
			prebuilt := f.client.list[0]
			prebuilt.CreatedAt, prebuilt.LastModifiedAt = now.Add(time.Millisecond), time.Time{}
			zeta := analyzerWithJSON("zeta", `{"analyzerId":"zeta"}`)
			zeta.LastModifiedAt = now.Add(time.Millisecond)
			f.client.list = []cuclient.Analyzer{prebuilt, alpha, zeta}
			stdout, _, err := f.execute(t.Context(), "list", "--kind", tt.kind, "--sort-by", tt.sort)
			require.NoError(t, err)
			var result []struct {
				AnalyzerID string `json:"analyzerId"`
			}
			require.NoError(t, json.Unmarshal([]byte(stdout), &result))
			actual := make([]string, 0, len(result))
			for _, item := range result {
				actual = append(actual, item.AnalyzerID)
			}
			assert.Equal(t, tt.want, actual)
			assert.Equal(t, "prebuilt-document", f.client.list[0].ID, "sorting must not mutate client-owned storage")
			assert.Equal(t, []string{"list"}, f.client.calls)
		})
	}
}

func TestAnalyzerListEmptyAndStable(t *testing.T) {
	f := newAnalyzerFixture(t)
	f.client.list = nil
	stdout, _, err := f.execute(t.Context(), "list")
	require.NoError(t, err)
	assert.Equal(t, "[]\n", stdout)
	f.client.list = []cuclient.Analyzer{
		analyzerWithJSON("zeta", `{"analyzerId":"zeta"}`),
		analyzerWithJSON("alpha", `{"analyzerId":"alpha"}`),
	}
	stdout, _, err = f.execute(t.Context(), "list", "--sort-by", "createdAt")
	require.NoError(t, err)
	assert.Less(t, strings.Index(stdout, "zeta"), strings.Index(stdout, "alpha"), "equal sort keys retain service order")
}

func TestCreateAnalyzerMappingAndWarnings(t *testing.T) {
	f := newAnalyzerFixture(t)
	body := `{"baseAnalyzerId":"prebuilt-document","future":{"integer":9007199254740993123456789}}`
	f.client.analyzer.JSON = json.RawMessage(
		`{"analyzerId":"invoice_v1","warnings":[{"code":"Warning","message":"Service warning"}],"future":9007199254740993}`)
	stdout, stderr, err := f.execute(t.Context(), "create", "invoice_v1", "-s", schemaFile(t, body),
		"--endpoint", "https://explicit.services.ai.azure.com", "--api-version", cuclient.PreviewAPIVersion)
	require.NoError(t, err)
	assert.JSONEq(t, string(f.client.analyzer.JSON), stdout)
	assert.Contains(t, stdout, "9007199254740993")
	assert.Empty(t, stderr)
	assert.Equal(t, "invoice_v1", f.client.create.AnalyzerID)
	assert.Contains(t, string(f.client.create.Definition), "9007199254740993123456789")
	assert.JSONEq(t, body, string(f.client.create.Definition))
	assert.Equal(t, config.Options{
		Endpoint: "https://explicit.services.ai.azure.com", APIVersion: cuclient.PreviewAPIVersion,
	}, f.options)
	assert.Equal(t, []string{"create:invoice_v1"}, f.client.calls, "create must not preflight or replace existing analyzers")
}

func TestCreateSchemaAndVersionFailures(t *testing.T) {
	for _, tt := range []struct {
		body, version string
		offline       bool
	}{
		{`not-json`, "", true}, {`[]`, "", true},
		{`{"fieldSchema":{"fields":{"Total":{"type":"invalid"}}}}`, "", true},
		{`{"apiVersion":"2026-06-01-preview"}`, cuclient.DefaultAPIVersion, false},
		{`{"apiVersion":"2025-11-01"}`, cuclient.PreviewAPIVersion, false},
	} {
		t.Run(tt.body+tt.version, func(t *testing.T) {
			f := newAnalyzerFixture(t)
			if tt.version != "" {
				f.version = tt.version
			}
			stdout, _, err := f.execute(t.Context(), "create", "invoice", "-s", schemaFile(t, tt.body))
			require.Error(t, err)
			assert.Empty(t, stdout)
			assert.Empty(t, f.client.calls)
			if tt.offline {
				assert.Zero(t, f.opens)
			} else {
				assert.Equal(t, 1, f.closes)
			}
		})
	}
	f := newAnalyzerFixture(t)
	_, _, err := f.execute(t.Context(), "create", "invoice", "-s", filepath.Join(t.TempDir(), "missing.json"))
	require.Error(t, err)
	local, ok := errors.AsType[*azdext.LocalError](err)
	require.True(t, ok)
	assert.Equal(t, "schema_read_failed", local.Code)
	assert.Zero(t, f.opens)
}

func TestCreateResolvesPinnedVersion(t *testing.T) {
	f := newAnalyzerFixture(t)
	f.version = cuclient.PreviewAPIVersion
	_, _, err := f.execute(t.Context(), "create", "invoice", "-s",
		schemaFile(t, `{"apiVersion":"2026-06-01-preview"}`))
	require.NoError(t, err)
	assert.Equal(t, []string{"create:invoice"}, f.client.calls)
}

func TestCreateSchemaRelativeToCwd(t *testing.T) {
	f := newAnalyzerFixture(t)
	parent := t.TempDir()
	t.Chdir(parent)
	target := filepath.Join(parent, "project")
	require.NoError(t, os.Mkdir(target, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(target, "schema.json"), []byte(`{}`), 0o600))
	_, _, err := f.execute(t.Context(), "create", "invoice", "-s", "schema.json", "--cwd", target)
	require.NoError(t, err)
	assert.Equal(t, []string{"create:invoice"}, f.client.calls)
}

func TestAnalyzerDeletionConfirmation(t *testing.T) {
	for _, tt := range []struct {
		name      string
		args      []string
		confirmed bool
		wantError string
		prompts   int
	}{
		{"interactive yes", nil, true, "", 1},
		{"interactive no", nil, false, exterrors.CodeCancelled, 1},
		{"yes flag", []string{"--yes"}, false, "", 0},
		{"noninteractive missing yes", []string{"--no-prompt"}, true, "confirmation_required", 0},
		{"noninteractive yes", []string{"--no-prompt", "-y"}, false, "", 0},
		{"explicit no", []string{"--no-prompt", "--yes=false"}, true, "confirmation_required", 0},
	} {
		t.Run(tt.name, func(t *testing.T) {
			f := newAnalyzerFixture(t)
			f.confirmed = tt.confirmed
			stdout, _, err := f.execute(t.Context(), append([]string{"delete", "alpha"}, tt.args...)...)
			if tt.wantError != "" {
				local, ok := errors.AsType[*azdext.LocalError](err)
				require.True(t, ok)
				assert.Equal(t, tt.wantError, local.Code)
				assert.Empty(t, f.client.calls)
				assert.Empty(t, stdout)
			} else {
				require.NoError(t, err)
				assert.Equal(t, []string{"get:alpha", "delete:alpha"}, f.client.calls)
			}
			assert.Len(t, f.confirmations, tt.prompts)
			if tt.prompts != 0 {
				assert.Equal(t, "Delete analyzer 'alpha'?", f.confirmations[0])
			}
			assert.Equal(t, f.opens, f.closes)
		})
	}
}

func TestAnalyzerNoPromptEnvironmentAndOverride(t *testing.T) {
	f := newAnalyzerFixture(t)
	t.Setenv("AZD_NO_PROMPT", "true")
	_, _, err := f.execute(t.Context(), "delete", "alpha")
	require.Error(t, err)
	assert.Zero(t, f.opens)
	_, _, err = f.execute(t.Context(), "delete", "alpha", "--no-prompt=false")
	require.NoError(t, err)
	assert.Len(t, f.confirmations, 1)
}

func TestAnalyzerServiceErrors(t *testing.T) {
	for _, kind := range []cuclient.ErrorKind{
		cuclient.ErrorNotFound, cuclient.ErrorConflict, cuclient.ErrorThrottled, cuclient.ErrorAuthentication,
		cuclient.ErrorTimeout, cuclient.ErrorService,
	} {
		for _, action := range []string{"list", "show", "create", "delete"} {
			t.Run(string(kind)+"/"+action, func(t *testing.T) {
				f := newAnalyzerFixture(t)
				statusCode := map[cuclient.ErrorKind]int{
					cuclient.ErrorNotFound: 404, cuclient.ErrorConflict: 409, cuclient.ErrorThrottled: 429,
					cuclient.ErrorAuthentication: 403, cuclient.ErrorService: 503,
				}[kind]
				f.client.err = &cuclient.Error{Kind: kind, Code: "SafeCode", Message: "Safe message", StatusCode: statusCode}
				args := []string{action}
				if action != "list" {
					args = append(args, "alpha")
				}
				if action == "create" {
					args = append(args, "-s", schemaFile(t, `{}`))
				}
				if action == "delete" {
					args = append(args, "-y")
				}
				stdout, stderr, err := f.execute(t.Context(), args...)
				require.Error(t, err)
				if kind == cuclient.ErrorAuthentication || kind == cuclient.ErrorTimeout {
					local, ok := errors.AsType[*azdext.LocalError](err)
					require.True(t, ok)
					if kind == cuclient.ErrorAuthentication {
						assert.Equal(t, azdext.LocalErrorCategoryAuth, local.Category)
						assert.Equal(t, "SafeCode", local.Code)
					} else {
						assert.Equal(t, exterrors.CodeDeadlineExceeded, local.Code)
					}
				} else {
					service, ok := errors.AsType[*azdext.ServiceError](err)
					require.True(t, ok)
					assert.Equal(t, statusCode, service.StatusCode)
					assert.Equal(t, "SafeCode", service.ErrorCode)
				}
				assert.Empty(t, stdout, "never publish partial or success-shaped output")
				assert.Empty(t, stderr)
				assert.Equal(t, 1, f.closes)
				assert.Len(t, f.client.calls, 1, "a failed delete preflight must not delete")
			})
		}
	}
}

func TestAnalyzerCancellation(t *testing.T) {
	for _, stage := range []string{"before", "list", "show", "create", "get", "prompt", "delete"} {
		t.Run(stage, func(t *testing.T) {
			f := newAnalyzerFixture(t)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			action := stage
			if stage == "before" {
				cancel()
				action = "list"
			} else if stage == "get" || stage == "prompt" || stage == "delete" {
				action = "delete"
			}
			f.client.onCall = func(callCtx context.Context, call string) {
				assert.Equal(t, ctx.Done(), callCtx.Done())
				if stage != "prompt" && (stage != "delete" || call == "delete:alpha") {
					cancel()
				}
			}
			if stage == "prompt" {
				f.onPrompt = cancel
			}
			args := []string{action}
			if action != "list" {
				args = append(args, "alpha")
			}
			if action == "create" {
				args = append(args, "-s", schemaFile(t, `{}`))
			}
			if action == "delete" && stage != "prompt" {
				args = append(args, "--yes")
			}
			stdout, _, err := f.execute(ctx, args...)
			require.Error(t, err)
			assert.Empty(t, stdout)
			assert.ErrorIs(t, err, context.Canceled)
			if stage == "get" || stage == "prompt" {
				assert.NotContains(t, f.client.calls, "delete:alpha")
			}
			assert.Equal(t, f.opens, f.closes)
		})
	}
}

func TestAnalyzerInvalidClientOutput(t *testing.T) {
	for _, action := range []string{"list", "show", "create"} {
		t.Run(action, func(t *testing.T) {
			f := newAnalyzerFixture(t)
			f.client.analyzer.JSON = json.RawMessage(`[]`)
			f.client.list = []cuclient.Analyzer{f.client.analyzer}
			args := []string{action}
			if action != "list" {
				args = append(args, "alpha")
			}
			if action == "create" {
				args = append(args, "-s", schemaFile(t, `{}`))
			}
			stdout, _, err := f.execute(t.Context(), args...)
			require.Error(t, err)
			assert.Empty(t, stdout)
			assert.Equal(t, 1, f.closes)
		})
	}
	f := newAnalyzerFixture(t)
	f.client.analyzer.Status = "Failed"
	stdout, _, err := f.execute(t.Context(), "create", "alpha", "-s", schemaFile(t, `{}`))
	require.Error(t, err)
	assert.Empty(t, stdout)
}

func TestAnalyzerFactoryPromptAndWriterFailures(t *testing.T) {
	f := newAnalyzerFixture(t)
	f.openErr = exterrors.HostUnavailable()
	_, _, err := f.execute(t.Context(), "list")
	require.Error(t, err)
	assert.Empty(t, f.client.calls)
	assert.Zero(t, f.closes)

	f = newAnalyzerFixture(t)
	f.promptErr = context.DeadlineExceeded
	stdout, _, err := f.execute(t.Context(), "delete", "alpha")
	assert.ErrorIs(t, err, context.DeadlineExceeded)
	assert.Empty(t, stdout)
	assert.Empty(t, f.client.calls)
	assert.Equal(t, 1, f.closes)

	f = newAnalyzerFixture(t)
	root := f.root()
	root.SetOut(failingWriter{})
	root.SetArgs([]string{"analyzer", "list"})
	require.Error(t, root.ExecuteContext(t.Context()))
	assert.Equal(t, 1, f.closes)
}

func TestDeleteWriteFailureAfterSuccessfulPreflight(t *testing.T) {
	f := newAnalyzerFixture(t)
	f.client.onCall = func(_ context.Context, call string) {
		if call == "delete:alpha" {
			f.client.err = &cuclient.Error{Kind: cuclient.ErrorService, Code: "DeleteFailed", StatusCode: 503,
				Message: "The deletion failed."}
		}
	}
	stdout, stderr, err := f.execute(t.Context(), "delete", "alpha", "--yes")
	require.Error(t, err)
	service, ok := errors.AsType[*azdext.ServiceError](err)
	require.True(t, ok)
	assert.Equal(t, "DeleteFailed", service.ErrorCode)
	assert.Equal(t, 503, service.StatusCode)
	assert.Equal(t, []string{"get:alpha", "delete:alpha"}, f.client.calls)
	assert.Empty(t, stdout)
	assert.Empty(t, stderr)
	assert.Equal(t, 1, f.closes)
}

func TestAnalyzerHelpAndMetadataAreOffline(t *testing.T) {
	t.Setenv("AZD_SERVER", "")
	t.Setenv("AZD_ACCESS_TOKEN", "")
	for _, action := range []string{"list", "show", "create", "delete"} {
		stdout, stderr, err := executeRoot(t, "analyzer", action, "--help")
		require.NoError(t, err)
		assert.Contains(t, stdout, "--endpoint")
		assert.Contains(t, stdout, "azd ai cu analyzer "+action)
		assert.Empty(t, stderr)
	}
	stdout, stderr, err := executeRoot(t, "metadata")
	require.NoError(t, err)
	var document extensions.ExtensionCommandMetadata
	require.NoError(t, json.Unmarshal([]byte(stdout), &document))
	for _, action := range []string{"list", "show", "create", "delete"} {
		argv := []string{"analyzer", "--environment", "show", action, "-o", "json"}
		command := extensions.ResolveCommand(&document, argv)
		require.NotNil(t, command)
		assert.Equal(t, []string{"analyzer", action}, command.Name)
		require.NotNil(t, command.Launch)
		assert.True(t, command.Launch.NoAzdEnvironment)
	}
	assert.Empty(t, stderr)
}
