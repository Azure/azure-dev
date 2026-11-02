// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"azure.ai.content-understanding/internal/config"
	"azure.ai.content-understanding/internal/cuclient"
	"azure.ai.content-understanding/internal/schema"
	"azure.ai.content-understanding/internal/testutil"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/extensions"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func executeSchemaCreate(f *analyzeFixture, ctx context.Context, args ...string) (string, string, error) {
	root := f.root()
	var stdout, stderr bytes.Buffer
	root.SetOut(&stdout)
	root.SetErr(&stderr)
	root.SetArgs(append([]string{"analyzer", "schema", "create"}, args...))
	err := root.ExecuteContext(ctx)
	return stdout.String(), stderr.String(), err
}

func suggestionResponse(fields string) cuclient.AnalyzeResponse {
	return cuclient.AnalyzeResponse{Result: cuclient.AnalysisResult{Contents: []cuclient.Content{{
		Fields: map[string]json.RawMessage{"schema": json.RawMessage(`{"valueJson":` + fields + `}`)},
	}}}}
}

func TestSchemaCreateOfflineTemplatesVersionAndOutput(t *testing.T) {
	for _, kind := range []string{"extraction", "classification"} {
		for _, format := range []string{"default", "json", "none"} {
			t.Run(kind+"/"+format, func(t *testing.T) {
				f := newAnalyzeFixture(t)
				t.Setenv("AZD_SERVER", "")
				t.Setenv("AZD_ACCESS_TOKEN", "")
				t.Setenv("CU_API_KEY", "private-key-not-read")
				t.Setenv("CU_ENDPOINT", "invalid-not-read")
				t.Setenv("CU_MODEL_DEPLOYMENTS", "invalid-not-read")
				t.Setenv("CU_API_VERSION", cuclient.PreviewAPIVersion)
				output := filepath.Join(testutil.TempDir(t), "new", "schema.json")
				stdout, stderr, err := executeSchemaCreate(f, t.Context(),
					"--from-template", "--type", kind, "-a", "invoice_v1", "--modality", "video",
					"--base", "prebuilt-document", "--output-file", output, "-o", format)
				require.NoError(t, err)
				assert.Empty(t, stderr)
				assert.Zero(t, f.opens)
				assert.Zero(t, f.client.calls.Load())
				body, err := os.ReadFile(output) // #nosec G304 -- Reads only this test's private temporary output.
				require.NoError(t, err)
				var definition schema.Definition
				require.NoError(t, json.Unmarshal(body, &definition))
				assert.Equal(t, "invoice_v1", definition.AnalyzerID)
				assert.Equal(t, cuclient.PreviewAPIVersion, definition.APIVersion)
				assert.Equal(t, "prebuilt-document", definition.BaseAnalyzerID)
				assert.Equal(t, "gpt-5.2", definition.Models.Completion)
				assert.Equal(t, kind == "classification", definition.Config.EnableSegment)
				if format == "none" {
					assert.Empty(t, stdout)
				} else {
					assert.JSONEq(t, string(body), stdout)
				}
			})
		}
	}
	f := newAnalyzeFixture(t)
	t.Setenv("CU_API_VERSION", "invalid")
	stdout, stderr, err := executeSchemaCreate(f, t.Context(), "--api-version", cuclient.DefaultAPIVersion)
	require.NoError(t, err)
	assert.Empty(t, stderr)
	assert.Contains(t, stdout, `"analyzerId": "my_analyzer_v1"`)
	assert.Contains(t, stdout, cuclient.DefaultAPIVersion)
	assert.Zero(t, f.opens)
	stdout, _, err = executeSchemaCreate(f, t.Context())
	require.Error(t, err)
	assert.Empty(t, stdout)
}

func TestSchemaCreateSampleMappingAndSafeDiagnostics(t *testing.T) {
	for _, empty := range []bool{false, true} {
		for _, format := range []string{"json", "none"} {
			t.Run(format+map[bool]string{true: "/empty", false: "/fields"}[empty], func(t *testing.T) {
				f := newAnalyzeFixture(t)
				sample := filepath.Join(testutil.TempDir(t), "invoice[1].PDF")
				require.NoError(t, os.WriteFile(sample, []byte("sample bytes"), 0o600))
				output := filepath.Join(testutil.TempDir(t), "schema.json")
				var reader io.ReadSeeker
				f.client.call = func(
					_ context.Context, request cuclient.AnalyzeRequest,
				) (cuclient.AnalyzeResponse, error) {
					assert.Equal(t, schema.SuggestionAnalyzer, request.AnalyzerID)
					assert.Equal(t, cuclient.AnalysisModeAsync, request.Mode)
					assert.Equal(t, "application/pdf", request.Input.ContentType)
					reader = request.Input.Body
					data, err := io.ReadAll(reader)
					require.NoError(t, err)
					assert.Equal(t, "sample bytes", string(data))
					fields := `{"Total":{"type":"number","future":9007199254740993123456789}}`
					if empty {
						fields = `{}`
					}
					result := suggestionResponse(fields)
					result.Result.Warnings = []cuclient.Diagnostic{{Code: "Review", Message: "private-service-detail"}}
					return result, nil
				}
				stdout, stderr, err := executeSchemaCreate(f, t.Context(),
					"--from-sample", sample, "-n", "invoice_v1", "--output-file", output,
					"--endpoint", "https://explicit.services.ai.azure.com", "--api-version", cuclient.PreviewAPIVersion,
					"--no-prompt", "-o", format)
				require.NoError(t, err)
				assert.Equal(t, 1, f.opens)
				assert.Equal(t, 1, f.closes)
				assert.Empty(t, f.prompts)
				assert.Equal(t, schema.SuggestionAnalyzer, f.options.DefaultAnalyzer)
				assert.Equal(t, "https://explicit.services.ai.azure.com", f.options.Endpoint)
				assert.Equal(t, cuclient.PreviewAPIVersion, f.options.APIVersion)
				assert.Contains(t, stderr, "1 schema-suggestion warning")
				assert.NotContains(t, stdout+stderr, "private-service-detail")
				assert.Equal(t, empty, strings.Contains(stderr, "No suggested fields"))
				body, err := os.ReadFile(output) // #nosec G304 -- Reads only this test's private temporary output.
				require.NoError(t, err)
				assert.NotContains(t, string(body), `"warnings"`)
				if !empty {
					assert.Contains(t, string(body), "9007199254740993123456789")
					assert.Contains(t, string(body), "TODO: describe the 'Total' field.")
					assert.Contains(t, string(body), "invoice[1].PDF")
				}
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
}

func TestSchemaCreatePreflightAndOverwritePolicies(t *testing.T) {
	f := newAnalyzeFixture(t)
	sample := filepath.Join(testutil.TempDir(t), "sample.pdf")
	require.NoError(t, os.WriteFile(sample, []byte("sample bytes"), 0o600))
	output := schemaFile(t, "keep original")
	for _, args := range [][]string{
		{"positional"}, {"--name", ""}, {"--name", "bad-id"}, {"--name", "bad/id"}, {"--modality", "invalid"},
		{"--type", "invalid"}, {"--type", ""}, {"--base", ""}, {"--base", "bad/base"},
		{"--output-file", ""}, {"--from-sample", ""}, {"--from-sample", "https://example.com/sample.pdf"},
		{"--from-sample", filepath.Dir(sample)}, {"--from-sample", sample + ".missing"},
		{"--from-sample", sample, "--from-template"}, {"--from-sample", sample, "--type", "classification"},
		{"--from-sample", sample, "--modality", "audio"}, {"--from-sample", sample, "--base", "prebuilt-document"},
		{"--from-sample", sample, "--output-file", sample, "--force"},
		{"--from-sample", sample, "--output-file", filepath.Dir(sample), "--force"},
		{"--from-sample", sample, "--output-file", output},
		{"--output-file", output}, {"--api-version", "invalid"},
		{"--json"}, {"--api-key", "private-key"}, {"--profile", "private-profile"}, {"--inline"}, {"--yes"},
	} {
		stdout, _, err := executeSchemaCreate(f, t.Context(), args...)
		require.Error(t, err, "%v", args)
		assert.Empty(t, stdout)
		assert.Zero(t, f.opens)
		assert.Zero(t, f.client.calls.Load())
	}
	stdout, stderr, err := executeSchemaCreate(f, t.Context(), "--output-file", output, "--force", "-o", "none")
	require.NoError(t, err)
	assert.Empty(t, stdout+stderr)
	body, err := os.ReadFile(output) // #nosec G304 -- Reads only this test's private temporary output.
	require.NoError(t, err)
	assert.Contains(t, string(body), `"analyzerId": "my_analyzer_v1"`)
	body, err = os.ReadFile(sample) // #nosec G304 -- Reads only this test's private temporary sample.
	require.NoError(t, err)
	assert.Equal(t, "sample bytes", string(body))
}

func TestSchemaCreateRejectsSampleAliasesAndUnsafeOutputParents(t *testing.T) {
	for _, kind := range []string{"hardlink", "symlink", "parent-symlink", "parent-file"} {
		t.Run(kind, func(t *testing.T) {
			f := newAnalyzeFixture(t)
			sample := filepath.Join(testutil.TempDir(t), "sample.pdf")
			require.NoError(t, os.WriteFile(sample, []byte("sample bytes"), 0o600))
			parent := testutil.TempDir(t)
			output := filepath.Join(parent, "schema.json")
			switch kind {
			case "hardlink":
				require.NoError(t, os.Link(sample, output))
			case "symlink":
				if err := os.Symlink(sample, output); err != nil {
					t.Skipf("symlink creation unavailable: %v", err)
				}
			case "parent-symlink":
				target := filepath.Join(parent, "linked")
				if err := os.Symlink(testutil.TempDir(t), target); err != nil {
					t.Skipf("symlink creation unavailable: %v", err)
				}
				output = filepath.Join(target, "schema.json")
			case "parent-file":
				file := schemaFile(t, "parent is a file")
				output = filepath.Join(file, "schema.json")
			}
			stdout, _, err := executeSchemaCreate(f, t.Context(),
				"--from-sample", sample, "--output-file", output, "--force")
			require.Error(t, err)
			assert.Empty(t, stdout)
			assert.Zero(t, f.opens)
		})
	}
}

func TestSchemaCreateFailureCancellationAndLateOutputConflict(t *testing.T) {
	for _, mode := range []string{"open", "service", "cancelled", "malformed", "late-conflict", "late-alias"} {
		t.Run(mode, func(t *testing.T) {
			f := newAnalyzeFixture(t)
			sample := filepath.Join(testutil.TempDir(t), "sample.pdf")
			require.NoError(t, os.WriteFile(sample, []byte("sample bytes"), 0o600))
			output := filepath.Join(testutil.TempDir(t), "new", "schema.json")
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if mode == "open" {
				f.openErr = errors.New("private setup detail")
			}
			f.client.call = func(context.Context, cuclient.AnalyzeRequest) (cuclient.AnalyzeResponse, error) {
				switch mode {
				case "service":
					return cuclient.AnalyzeResponse{}, errors.New("private backend detail")
				case "cancelled":
					cancel()
				case "malformed":
					return cuclient.AnalyzeResponse{}, nil
				case "late-conflict", "late-alias":
					require.NoError(t, os.MkdirAll(filepath.Dir(output), 0o700))
					if mode == "late-alias" {
						require.NoError(t, os.Link(sample, output))
					} else {
						require.NoError(t, os.WriteFile(output, []byte("keep concurrent output"), 0o600))
					}
				}
				return suggestionResponse(`{"Total":{"type":"number"}}`), nil
			}
			args := []string{"--from-sample", sample, "--output-file", output}
			if mode == "late-alias" {
				args = append(args, "--force")
			}
			stdout, stderr, err := executeSchemaCreate(f, ctx, args...)
			require.Error(t, err)
			assert.Empty(t, stdout)
			assert.NotContains(t, stderr+err.Error(), "private")
			if mode == "cancelled" {
				assert.ErrorIs(t, err, context.Canceled)
			}
			if mode == "late-conflict" {
				body, err := os.ReadFile(output) // #nosec G304 -- Reads only this test's concurrent output.
				require.NoError(t, err)
				assert.Equal(t, "keep concurrent output", string(body))
			} else if mode != "late-alias" {
				_, err := os.Stat(filepath.Dir(output))
				assert.ErrorIs(t, err, os.ErrNotExist)
			}
			if mode != "open" {
				assert.Equal(t, 1, f.closes)
			}
		})
	}
	f := newAnalyzeFixture(t)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	stdout, _, err := executeSchemaCreate(f, ctx)
	assert.ErrorIs(t, err, context.Canceled)
	assert.Empty(t, stdout)
	assert.Zero(t, f.opens)
}

func TestSchemaCreateOutputErrorsAndMetadata(t *testing.T) {
	for _, mode := range []string{"stdout", "fallback-warning", "service-warning"} {
		t.Run(mode, func(t *testing.T) {
			f := newAnalyzeFixture(t)
			root := f.root()
			args := []string{"analyzer", "schema", "create"}
			if mode == "stdout" {
				root.SetOut(failingWriter{})
			} else {
				sample := filepath.Join(testutil.TempDir(t), "sample.pdf")
				require.NoError(t, os.WriteFile(sample, []byte("sample"), 0o600))
				args = append(args, "--from-sample", sample)
				root.SetErr(failingWriter{})
				f.client.call = func(context.Context, cuclient.AnalyzeRequest) (cuclient.AnalyzeResponse, error) {
					response := suggestionResponse(`{}`)
					if mode == "service-warning" {
						response = suggestionResponse(`{"Total":{"type":"number"}}`)
						response.Result.Warnings = []cuclient.Diagnostic{{Code: "Review"}}
					}
					return response, nil
				}
			}
			root.SetArgs(args)
			require.Error(t, root.ExecuteContext(t.Context()))
		})
	}
	stdout, stderr, err := executeRoot(t, "metadata")
	require.NoError(t, err)
	assert.Empty(t, stderr)
	var metadata extensions.ExtensionCommandMetadata
	require.NoError(t, json.Unmarshal([]byte(stdout), &metadata))
	command := extensions.ResolveCommand(&metadata, []string{"analyzer", "schema", "create"})
	require.NotNil(t, command)
	require.NotNil(t, command.Launch)
	assert.True(t, command.Launch.NoAzdEnvironment)
	root := NewRootCommand()
	require.NoError(t, azdext.ValidateNoReservedFlagConflicts(root))
}

func TestSchemaCreateRelativeCwdAndRetainedFileOnStdoutFailure(t *testing.T) {
	f := newAnalyzeFixture(t)
	working := testutil.TempDir(t)
	t.Chdir(working)
	project := filepath.Join(working, "project")
	require.NoError(t, os.Mkdir(project, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(project, "sample.pdf"), []byte("sample"), 0o600))
	f.client.call = func(context.Context, cuclient.AnalyzeRequest) (cuclient.AnalyzeResponse, error) {
		return suggestionResponse(`{"Total":{"type":"number"}}`), nil
	}
	stdout, _, err := executeSchemaCreate(f, t.Context(),
		"--cwd", project, "--from-sample", "sample.pdf", "--output-file", "generated/schema.json")
	require.NoError(t, err)
	assert.Contains(t, stdout, "Suggested from sample 'sample.pdf'")
	output := filepath.Join(project, "generated", "schema.json")
	assert.FileExists(t, output)
	_, err = os.Stat(filepath.Join(working, "generated"))
	assert.ErrorIs(t, err, os.ErrNotExist)
	root := f.root()
	root.SetOut(failingWriter{})
	output = filepath.Join(testutil.TempDir(t), "schema.json")
	root.SetArgs([]string{"analyzer", "schema", "create", "--output-file", output})
	require.Error(t, root.ExecuteContext(t.Context()))
	assert.FileExists(t, output)
}

func TestSchemaCreateCommandThroughCUPipeline(t *testing.T) {
	for _, version := range []string{cuclient.DefaultAPIVersion, cuclient.PreviewAPIVersion} {
		t.Run(version, func(t *testing.T) {
			_ = newAnalyzeFixture(t)
			credential := &copyWireCredential{}
			calls := 0
			transport := copyWireTransport(func(request *http.Request) (*http.Response, error) {
				calls++
				assert.Equal(t, version, request.URL.Query().Get("api-version"))
				assert.True(t, strings.HasPrefix(request.Header.Get("Authorization"), "Bearer "))
				result := &http.Response{StatusCode: 200, Header: http.Header{}, Request: request}
				body := `{"id":"schema-operation","status":"Succeeded","result":{"analyzerId":` +
					`"prebuilt-documentFieldSchema","apiVersion":"` + version + `","contents":[` +
					`{"kind":"document","mimeType":"application/pdf","fields":{"schema":{"type":"json",` +
					`"valueJson":{"Total":{"type":"number","future":9007199254740993123456789}}}}}]}}`
				if calls == 1 {
					assert.Equal(t, http.MethodPost, request.Method)
					assert.Equal(t, "/contentunderstanding/analyzers/prebuilt-documentFieldSchema:analyzeBinary",
						request.URL.Path)
					assert.Equal(t, "application/pdf", request.Header.Get("Content-Type"))
					data, err := io.ReadAll(request.Body)
					require.NoError(t, err)
					assert.Equal(t, "sample bytes", string(data))
					result.StatusCode = http.StatusAccepted
					result.Header.Set("Operation-Location", "https://resource.services.ai.azure.com/contentunderstanding/"+
						"analyzerResults/schema-operation?api-version="+version)
					body = `{"id":"schema-operation","status":"Running"}`
				} else {
					assert.Equal(t, 2, calls)
					assert.Equal(t, http.MethodGet, request.Method)
					assert.Equal(t, "/contentunderstanding/analyzerResults/schema-operation", request.URL.Path)
				}
				result.Body = io.NopCloser(strings.NewReader(body))
				return result, nil
			})
			client, err := cuclient.NewRESTTransport("https://resource.services.ai.azure.com", credential,
				&cuclient.TransportOptions{APIVersion: version, Transport: transport})
			require.NoError(t, err)
			root := newRootCommand(func(
				_ *cobra.Command, _ *azdext.ExtensionContext, options config.Options,
			) (*serviceSession, error) {
				assert.Equal(t, version, options.APIVersion)
				assert.Equal(t, schema.SuggestionAnalyzer, options.DefaultAnalyzer)
				return &serviceSession{client: client, apiVersion: version, close: func() {}}, nil
			})
			sample := filepath.Join(testutil.TempDir(t), "sample.pdf")
			require.NoError(t, os.WriteFile(sample, []byte("sample bytes"), 0o600))
			var stdout, stderr bytes.Buffer
			root.SetOut(&stdout)
			root.SetErr(&stderr)
			root.SetArgs([]string{"analyzer", "schema", "create",
				"--from-sample", sample, "--api-version", version})
			require.NoError(t, root.ExecuteContext(t.Context()))
			assert.Equal(t, 2, calls)
			assert.Empty(t, stderr.String())
			assert.Contains(t, stdout.String(), "9007199254740993123456789")
			assert.Contains(t, stdout.String(), "TODO: describe the 'Total' field.")
			assert.Contains(t, credential.scopes, "https://cognitiveservices.azure.com/.default")
		})
	}
}
