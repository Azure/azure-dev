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
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"azure.ai.content-understanding/internal/analysis"
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

const analyzeEnvelope = `{"status":"Succeeded","result":{"contents":[{"kind":"image","mimeType":"image/png",` +
	`"markdown":"Picture"}],"warnings":[{"code":"Review","message":"Check"}]},` +
	`"usage":{"tokens":{"model":9007199254740993}},"unknown":true}`

type analyzeFake struct {
	cuclient.Client
	calls atomic.Int32
	call  func(context.Context, cuclient.AnalyzeRequest) (cuclient.AnalyzeResponse, error)
}

func (f *analyzeFake) Analyze(ctx context.Context, request cuclient.AnalyzeRequest) (cuclient.AnalyzeResponse, error) {
	f.calls.Add(1)
	if f.call != nil {
		return f.call(ctx, request)
	}
	return cuclient.AnalyzeResponse{JSON: json.RawMessage(analyzeEnvelope)}, nil
}

type analyzeFixture struct {
	client        *analyzeFake
	analyzer      string
	options       config.Options
	opens, closes int
	prompts       []string
	confirmed     bool
	openErr       error
	promptErr     error
	onPrompt      func()
}

func newAnalyzeFixture(t *testing.T) *analyzeFixture {
	t.Helper()
	for _, key := range []string{"AZD_DEBUG", "AZD_NO_PROMPT", "AZD_ENVIRONMENT", "AZD_CWD"} {
		t.Setenv(key, "")
	}
	t.Setenv("CU_ON_EXISTS", "error")
	return &analyzeFixture{client: &analyzeFake{}, analyzer: "prebuilt-image", confirmed: true}
}

func (f *analyzeFixture) root() *cobra.Command {
	return newRootCommand(func(
		_ *cobra.Command, _ *azdext.ExtensionContext, options config.Options,
	) (*serviceSession, error) {
		f.opens++
		f.options = options
		if f.openErr != nil {
			return nil, f.openErr
		}
		analyzer := f.analyzer
		if options.DefaultAnalyzer != "" {
			analyzer = options.DefaultAnalyzer
		}
		version := options.APIVersion
		if version == "" {
			version = cuclient.DefaultAPIVersion
		}
		return &serviceSession{
			client: f.client, analyzer: analyzer, apiVersion: version,
			close: func() { f.closes++ },
			confirm: func(_ context.Context, stringValue string) (bool, error) {
				f.prompts = append(f.prompts, stringValue)
				if f.onPrompt != nil {
					f.onPrompt()
				}
				return f.confirmed, f.promptErr
			},
		}, nil
	})
}

func (f *analyzeFixture) execute(ctx context.Context, args ...string) (string, string, error) {
	root := f.root()
	var stdout, stderr bytes.Buffer
	root.SetOut(&stdout)
	root.SetErr(&stderr)
	root.SetArgs(append([]string{"analyze"}, args...))
	err := root.ExecuteContext(ctx)
	return stdout.String(), stderr.String(), err
}

func TestAnalyzeResolvedDefaultAnalyzer(t *testing.T) {
	for _, tt := range []struct {
		name, configured, want string
		flags                  []string
	}{
		{"unset", "", "prebuilt-layout", nil},
		{"configured", "prebuilt-invoice", "prebuilt-invoice", nil},
		{"explicit overrides configured", "prebuilt-invoice", "custom", []string{"--analyzer", "custom"}},
	} {
		for _, dryRun := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/dryRun=%t", tt.name, dryRun), func(t *testing.T) {
				startHost(t, &deploymentServer{get: func(context.Context) (*azdext.GetDeploymentContextResponse, error) {
					t.Error("endpoint-only analysis must not load deployment context")
					return nil, errors.New("unexpected deployment call")
				}})
				t.Chdir(testutil.TempDir(t))
				require.NoError(t, os.WriteFile("invoice.pdf", []byte("%PDF-1.7\n"), 0o600))
				t.Setenv("CU_ENDPOINT", "https://resource.services.ai.azure.com")
				t.Setenv("CU_DEFAULT_ANALYZER", tt.configured)
				client := &analyzeFake{call: func(
					_ context.Context, request cuclient.AnalyzeRequest,
				) (cuclient.AnalyzeResponse, error) {
					assert.Equal(t, tt.want, request.AnalyzerID)
					body, err := io.ReadAll(request.Input.Body)
					require.NoError(t, err)
					assert.Equal(t, "%PDF-1.7\n", string(body))
					return cuclient.AnalyzeResponse{JSON: json.RawMessage(analyzeEnvelope)}, nil
				}}
				root := newRootCommand(func(
					cmd *cobra.Command, extCtx *azdext.ExtensionContext, options config.Options,
				) (*serviceSession, error) {
					session, err := openServiceSession(cmd, extCtx, options)
					if err == nil {
						session.client = client
					}
					return session, err
				})
				args := append([]string{"analyze", "invoice.pdf"}, tt.flags...)
				if dryRun {
					args = append(args, "--dry-run")
				}
				root.SetArgs(args)
				var stdout, stderr bytes.Buffer
				root.SetOut(&stdout)
				root.SetErr(&stderr)
				require.NoError(t, root.ExecuteContext(t.Context()))
				assert.Empty(t, stderr.String())
				if dryRun {
					var plan struct {
						DryRun   bool   `json:"dryRun"`
						Analyzer string `json:"analyzerId"`
					}
					require.NoError(t, json.Unmarshal(stdout.Bytes(), &plan))
					assert.True(t, plan.DryRun)
					assert.Equal(t, tt.want, plan.Analyzer)
					assert.Zero(t, client.calls.Load())
				} else {
					assert.Contains(t, stdout.String(), "Picture")
					assert.False(t, json.Valid(stdout.Bytes()))
					assert.EqualValues(t, 1, client.calls.Load())
				}
			})
		}
	}
}

func TestAnalyzePublicFlagsRequestMappingAndOutput(t *testing.T) {
	for _, format := range []string{"default", "json", "none"} {
		t.Run(format, func(t *testing.T) {
			f := newAnalyzeFixture(t)
			path := schemaFile(t, "binary input")
			var body io.ReadSeeker
			f.client.call = func(ctx context.Context, request cuclient.AnalyzeRequest) (cuclient.AnalyzeResponse, error) {
				assert.Equal(t, "custom-analyzer", request.AnalyzerID)
				assert.Equal(t, cuclient.AnalysisModeInline, request.Mode)
				assert.Empty(t, request.Input.URL)
				assert.Equal(t, "application/octet-stream", request.Input.ContentType)
				bytes, err := io.ReadAll(request.Input.Body)
				require.NoError(t, err)
				assert.Equal(t, "binary input", string(bytes))
				body = request.Input.Body
				return cuclient.AnalyzeResponse{JSON: json.RawMessage(analyzeEnvelope)}, nil
			}
			stdout, stderr, err := f.execute(t.Context(),
				"--file", path, "--llm-input=false", "-a", "custom-analyzer", "-i", "-j", "1", "-o", format,
				"--endpoint", "https://explicit.services.ai.azure.com", "--api-version", cuclient.PreviewAPIVersion)
			require.NoError(t, err)
			assert.Empty(t, stderr)
			if format == "none" {
				assert.Empty(t, stdout)
			} else {
				assert.JSONEq(t, analyzeEnvelope, stdout)
				assert.Contains(t, stdout, "9007199254740993")
			}
			assert.Equal(t, 1, f.opens)
			assert.Equal(t, 1, f.closes)
			assert.Equal(t, "https://explicit.services.ai.azure.com", f.options.Endpoint)
			_, err = body.Read(make([]byte, 1))
			require.Error(t, err)
		})
	}
}

func TestAnalyzeUsageAndLLMStreaming(t *testing.T) {
	for _, flags := range [][]string{{"--usage"}, {"--llm-input"}, {"--llm-input", "--usage"}} {
		t.Run(strings.Join(flags, "/"), func(t *testing.T) {
			f := newAnalyzeFixture(t)
			stdout, stderr, err := f.execute(t.Context(), append([]string{"https://example.com/image.png"}, flags...)...)
			require.NoError(t, err)
			assert.Empty(t, stderr)
			assert.Contains(t, stdout, "Picture")
			if len(flags) == 1 && flags[0] == "--llm-input" {
				assert.Contains(t, stdout, "mimeType: image/png")
				assert.Contains(t, stdout, "warnings:")
				assert.False(t, json.Valid([]byte(stdout)))
			} else {
				var wrapper map[string]json.RawMessage
				require.NoError(t, json.Unmarshal([]byte(stdout), &wrapper))
				assert.Contains(t, wrapper, "result")
				assert.JSONEq(t, `{"tokens":{"model":9007199254740993}}`, string(wrapper["usage"]))
			}
		})
	}
}

func TestAnalyzeInvalidInputsFailBeforeSession(t *testing.T) {
	path := schemaFile(t, "input")
	directory := testutil.TempDir(t)
	for _, test := range []struct {
		name string
		args []string
	}{
		{"none", nil},
		{"mixed", []string{path, "--file", path}},
		{"empty source", []string{"--source="}},
		{"empty output", []string{path, "--output-file="}},
		{"duplicate", []string{"--file", path, "--file", path}},
		{"pattern mode", []string{path, "--pattern", "*.pdf"}},
		{"empty pattern", []string{"--source", directory, "--pattern="}},
		{"recursion", []string{path, "-r"}},
		{"bad url", []string{"--url", "https://user:private@example.com/a"}},
		{"bad analyzer", []string{path, "--analyzer", "bad/name"}},
		{"empty analyzer", []string{path, "--analyzer="}},
		{"zero concurrency", []string{path, "-j", "0"}},
		{"large concurrency", []string{path, "-j", "33"}},
		{"invalid integer", []string{path, "-j", "oops"}},
		{"confirmation dry run", []string{path, "--dry-run", "-y"}},
		{"bad policy", []string{path, "--on-existing", "replace"}},
		{"empty policy", []string{path, "--on-existing="}},
		{"output conflict", []string{path, "--output-file", "out", "-d", directory}},
		{"input overwrite", []string{path, "--output-file", path, "--on-existing", "reanalyze"}},
		{"json alias excluded", []string{path, "--json"}},
		{"key auth excluded", []string{path, "--api-key", "private"}},
		{"table excluded", []string{path, "-o", "table"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			f := newAnalyzeFixture(t)
			stdout, stderr, err := f.execute(t.Context(), test.args...)
			require.Error(t, err)
			assert.NotContains(t, err.Error(), "private")
			assert.Empty(t, stdout)
			assert.Empty(t, stderr)
			assert.Zero(t, f.opens)
			assert.Zero(t, f.client.calls.Load())
		})
	}
}

func TestAnalyzeDryRunRedactedNoWritesNoRequests(t *testing.T) {
	f := newAnalyzeFixture(t)
	directory := filepath.Join(testutil.TempDir(t), "not-created")
	report := filepath.Join(directory, "report.json")
	stdout, stderr, err := f.execute(t.Context(),
		"--url", "https://example.com/a.pdf?sig=secret-signature#secret-fragment", "--dry-run",
		"-d", directory, "--report-file", report, "-o", "json")
	require.NoError(t, err)
	assert.Empty(t, stderr)
	var plan struct {
		DryRun   bool   `json:"dryRun"`
		Analyzer string `json:"analyzerId"`
		Outputs  []struct {
			Input   string `json:"input"`
			Output  string `json:"output"`
			Exists  bool   `json:"exists"`
			Skipped bool   `json:"skipped"`
		} `json:"outputs"`
	}
	require.NoError(t, json.Unmarshal([]byte(stdout), &plan))
	assert.True(t, plan.DryRun)
	assert.Equal(t, "prebuilt-image", plan.Analyzer)
	require.Len(t, plan.Outputs, 1)
	assert.Equal(t, "https://example.com/a.pdf?REDACTED", plan.Outputs[0].Input)
	assert.Equal(t, filepath.Join(directory, "a.pdf.result.json"), plan.Outputs[0].Output)
	assert.NotContains(t, stdout, "secret-signature")
	assert.NotContains(t, stdout, "secret-fragment")
	assert.Empty(t, f.prompts)
	assert.Zero(t, f.client.calls.Load())
	_, err = os.Stat(directory)
	require.ErrorIs(t, err, fs.ErrNotExist)
	assert.Equal(t, f.opens, f.closes)
}

func TestAnalyzeExistingPolicyEnvironmentAndNoOutputStillWrites(t *testing.T) {
	for _, test := range []struct {
		env, flag string
		calls     int32
		wantErr   bool
	}{
		{"skip", "", 0, false},
		{"error", "skip", 0, false},
		{"skip", "reanalyze", 1, false},
		{"reanalyze", "", 1, false},
		{"error", "", 0, true},
		{"invalid", "", 0, true},
		{"", "", 0, true},
	} {
		t.Run(test.env+"/"+test.flag, func(t *testing.T) {
			f := newAnalyzeFixture(t)
			t.Setenv("CU_ON_EXISTS", test.env)
			path := schemaFile(t, "input")
			output := schemaFile(t, "existing")
			report := filepath.Join(testutil.TempDir(t), "report.json")
			args := []string{path, "--llm-input=false", "--output-file", output, "--report-file", report, "-o", "none"}
			if test.flag != "" {
				args = append(args, "--on-existing", test.flag)
			}
			stdout, stderr, err := f.execute(t.Context(), args...)
			if test.wantErr {
				require.Error(t, err)
				assert.Zero(t, f.opens)
			} else {
				require.NoError(t, err)
				_, err := os.Stat(report)
				require.NoError(t, err)
			}
			assert.Empty(t, stdout)
			assert.Empty(t, stderr)
			assert.Equal(t, test.calls, f.client.calls.Load())
			// #nosec G304 -- Reads only this test's private temporary output.
			body, err := os.ReadFile(output)
			require.NoError(t, err)
			if test.calls > 0 {
				assert.JSONEq(t, analyzeEnvelope, string(body))
			} else {
				assert.Equal(t, "existing", string(body))
			}
		})
	}
}

func TestAnalyzeCostConfirmationExactThreshold(t *testing.T) {
	directory := testutil.TempDir(t)
	for index := range 51 {
		require.NoError(t, os.WriteFile(filepath.Join(directory, fmt.Sprintf("%02d.pdf", index)), []byte("input"), 0o600))
	}
	for _, test := range []struct {
		name      string
		flags     []string
		confirmed bool
		prompts   int
		calls     int32
		wantErr   bool
	}{
		{"interactive yes", nil, true, 1, 51, false},
		{"interactive no", nil, false, 1, 0, true},
		{"noninteractive", []string{"--no-prompt"}, false, 0, 0, true},
		{"explicit yes", []string{"--no-prompt", "-y"}, false, 0, 51, false},
		{"dry run", []string{"--no-prompt", "--dry-run"}, false, 0, 0, false},
		{"exactly 50", []string{"--pattern", "[!5]*.pdf", "--no-prompt"}, false, 0, 50, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			f := newAnalyzeFixture(t)
			f.confirmed = test.confirmed
			args := []string{"--source", directory, "-d", testutil.TempDir(t)}
			stdout, stderr, err := f.execute(t.Context(), append(args, test.flags...)...)
			if test.wantErr {
				require.Error(t, err)
				assert.Empty(t, stdout)
			} else {
				require.NoError(t, err)
			}
			assert.Empty(t, stderr)
			assert.Equal(t, test.calls, f.client.calls.Load())
			require.Len(t, f.prompts, test.prompts)
			if test.prompts > 0 {
				assert.Equal(t, "Analyze 51 inputs with 'prebuilt-image'? This may incur cost.", f.prompts[0])
			}
		})
	}
}

func TestAnalyzeMissingConfigurationInlineGuardAndCancellation(t *testing.T) {
	path := schemaFile(t, "input")
	for _, mode := range []string{"analyzer", "inline", "open", "cancelled"} {
		t.Run(mode, func(t *testing.T) {
			f := newAnalyzeFixture(t)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			args := []string{path}
			switch mode {
			case "analyzer":
				f.analyzer = ""
			case "inline":
				args = append(args, "--inline")
			case "open":
				f.openErr = context.DeadlineExceeded
			case "cancelled":
				cancel()
			}
			stdout, stderr, err := f.execute(ctx, args...)
			require.Error(t, err)
			assert.Empty(t, stdout)
			assert.Empty(t, stderr)
			assert.Zero(t, f.client.calls.Load())
			if mode != "open" && mode != "cancelled" {
				assert.Equal(t, 1, f.closes)
			}
		})
	}
}

func TestAnalyzeFailureReportsAndCancellationUseErrorChannel(t *testing.T) {
	for _, cancelRequest := range []bool{false, true} {
		t.Run(fmt.Sprint(cancelRequest), func(t *testing.T) {
			f := newAnalyzeFixture(t)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			f.client.call = func(context.Context, cuclient.AnalyzeRequest) (cuclient.AnalyzeResponse, error) {
				if cancelRequest {
					cancel()
					return cuclient.AnalyzeResponse{}, context.Canceled
				}
				return cuclient.AnalyzeResponse{}, &cuclient.Error{
					Kind: cuclient.ErrorService, Code: "ServiceFailed", Message: "Safe service failure.",
				}
			}
			report := filepath.Join(testutil.TempDir(t), "report.json")
			stdout, stderr, err := f.execute(ctx, "https://example.com/input.pdf?sig=private", "--report-file", report)
			require.Error(t, err)
			assert.Empty(t, stdout)
			assert.Empty(t, stderr)
			assert.Equal(t, 1, f.closes)
			if cancelRequest {
				local, ok := errors.AsType[*azdext.LocalError](err)
				require.True(t, ok)
				assert.Equal(t, exterrors.CodeCancelled, local.Code)
			}
			// #nosec G304 -- Reads only this test's private temporary report.
			body, readErr := os.ReadFile(report)
			require.NoError(t, readErr)
			assert.Contains(t, string(body), `"failed": 1`)
			assert.NotContains(t, string(body), "private")
		})
	}
}

func TestAnalyzeHelpAndMetadataRemainOffline(t *testing.T) {
	for _, args := range [][]string{{"analyze", "--help"}, {"metadata"}} {
		f := newAnalyzeFixture(t)
		root := f.root()
		var stdout bytes.Buffer
		root.SetOut(&stdout)
		root.SetArgs(args)
		require.NoError(t, root.ExecuteContext(t.Context()))
		assert.Zero(t, f.opens)
		if args[0] == "metadata" {
			var document extensions.ExtensionCommandMetadata
			require.NoError(t, json.Unmarshal(stdout.Bytes(), &document))
			command := extensions.ResolveCommand(&document,
				[]string{"analyze", "--url", "https://example.com/a", "-o", "json"})
			require.NotNil(t, command)
			assert.True(t, command.Launch.NoAzdEnvironment)
		} else {
			for _, flag := range []string{"--file", "--source", "--url", "--pattern", "--recursive", "--analyzer",
				"--inline", "--usage", "--llm-input", "--output-file", "--output-dir", "--on-existing",
				"--dry-run", "--yes", "--report-file", "--concurrency"} {
				assert.Contains(t, stdout.String(), flag)
			}
		}
	}
}

func TestAnalyzeDefaultViewAndExplicitJSON(t *testing.T) {
	var envelope struct {
		Result json.RawMessage `json:"result"`
	}
	require.NoError(t, json.Unmarshal([]byte(analyzeEnvelope), &envelope))
	text, err := analysis.RenderLLM(envelope.Result)
	require.NoError(t, err)
	for _, tt := range []struct {
		name, format string
		flags        []string
		full         bool
	}{
		{"default", "default", nil, false},
		{"explicit LLM", "default", []string{"--llm-input"}, false},
		{"implicit full JSON", "json", nil, true},
		{"full default", "default", []string{"--llm-input=false"}, true},
		{"full JSON", "json", []string{"--llm-input=false"}, true},
		{"suppressed", "none", nil, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			f := newAnalyzeFixture(t)
			args := append([]string{"https://example.com/a.pdf", "--output", tt.format}, tt.flags...)
			stdout, stderr, err := f.execute(t.Context(), args...)
			require.NoError(t, err)
			assert.Empty(t, stderr)
			switch {
			case tt.format == "none":
				assert.Empty(t, stdout)
			case tt.full:
				assert.JSONEq(t, analyzeEnvelope, stdout)
			default:
				assert.Equal(t, text+"\n", stdout)
			}
			assert.EqualValues(t, 1, f.client.calls.Load())
		})
	}
}

func TestAnalyzeJSONUsageWrapsCompleteResponse(t *testing.T) {
	f := newAnalyzeFixture(t)
	stdout, stderr, err := f.execute(t.Context(), "https://example.com/a.pdf", "-o", "json", "--usage")
	require.NoError(t, err)
	assert.Empty(t, stderr)
	var wrapped struct {
		Result json.RawMessage `json:"result"`
		Usage  json.RawMessage `json:"usage"`
	}
	require.NoError(t, json.Unmarshal([]byte(stdout), &wrapped))
	assert.JSONEq(t, analyzeEnvelope, string(wrapped.Result))
	assert.JSONEq(t, `{"tokens":{"model":9007199254740993}}`, string(wrapped.Usage))
	assert.Contains(t, string(wrapped.Result), "9007199254740993")
	assert.Contains(t, string(wrapped.Usage), "9007199254740993")
	assert.EqualValues(t, 1, f.client.calls.Load())
}

func TestAnalyzeJSONViewConflictDoesNotOpenOrWrite(t *testing.T) {
	for _, flags := range [][]string{
		{"--output", "json", "--llm-input"},
		{"--output", "json", "--llm-input=true"},
		{"--llm-input", "-o", "json"},
		{"--llm-input=true", "-o", "json"},
	} {
		t.Run(strings.Join(flags, " "), func(t *testing.T) {
			f := newAnalyzeFixture(t)
			directory := filepath.Join(testutil.TempDir(t), "not-created")
			args := append([]string{"--url", "https://example.com/a.pdf",
				"--output-dir", directory, "--report-file", filepath.Join(directory, "report.json")}, flags...)
			stdout, stderr, err := f.execute(t.Context(), args...)
			require.ErrorContains(t, err, "--output json")
			assert.Contains(t, err.Error(), "--llm-input=true")
			assert.Empty(t, stdout)
			assert.Empty(t, stderr)
			assert.Zero(t, f.opens)
			assert.Zero(t, f.client.calls.Load())
			assert.Empty(t, f.prompts)
			_, err = os.Stat(directory)
			require.ErrorIs(t, err, fs.ErrNotExist)
		})
	}
}

func TestAnalyzeJSONSelectsSavedFullResponse(t *testing.T) {
	for _, batch := range []bool{false, true} {
		for _, usage := range []bool{false, true} {
			t.Run(fmt.Sprintf("batch=%t/usage=%t", batch, usage), func(t *testing.T) {
				f := newAnalyzeFixture(t)
				directory := testutil.TempDir(t)
				input := filepath.Join(directory, "invoice.pdf")
				require.NoError(t, os.WriteFile(input, []byte("input"), 0o600))
				output := filepath.Join(testutil.TempDir(t), "saved.md")
				report := filepath.Join(testutil.TempDir(t), "report.json")
				args := []string{input, "--output-file", output}
				if batch {
					output = filepath.Join(testutil.TempDir(t), "results")
					args = []string{"--source", directory, "--output-dir", output}
					output = filepath.Join(output, "invoice.pdf.result.json")
				}
				args = append(args, "-o", "json", "--report-file", report)
				if usage {
					args = append(args, "--usage")
				}
				stdout, stderr, err := f.execute(t.Context(), args...)
				require.NoError(t, err)
				assert.Empty(t, stderr)
				assert.True(t, json.Valid([]byte(stdout)), "saved results still produce JSON summaries")
				// #nosec G304 -- Reads only this test's private temporary output.
				body, err := os.ReadFile(output)
				require.NoError(t, err)
				assert.JSONEq(t, analyzeEnvelope, string(body), "preserve the full envelope regardless of file extension")
				// #nosec G304 -- Reads only this test's private temporary report.
				body, err = os.ReadFile(report)
				require.NoError(t, err)
				var summary struct {
					View string `json:"result_view"`
				}
				require.NoError(t, json.Unmarshal(body, &summary))
				assert.Equal(t, "full", summary.View)
				assert.EqualValues(t, 1, f.client.calls.Load())
			})
		}
	}
}

func TestAnalyzeDefaultSavedView(t *testing.T) {
	for _, full := range []bool{false, true} {
		for _, batch := range []bool{false, true} {
			t.Run(fmt.Sprintf("full=%t/batch=%t", full, batch), func(t *testing.T) {
				f := newAnalyzeFixture(t)
				directory := testutil.TempDir(t)
				input := filepath.Join(directory, "invoice.pdf")
				require.NoError(t, os.WriteFile(input, []byte("input"), 0o600))
				output := filepath.Join(testutil.TempDir(t), "saved")
				report := filepath.Join(testutil.TempDir(t), "report.json")
				args := []string{input, "--output-file", output, "--report-file", report, "--output", "none"}
				if batch {
					args = []string{"--source", directory, "--output-dir", output,
						"--report-file", report, "--output", "none"}
					suffix := ".result.md"
					if full {
						suffix = ".result.json"
					}
					output = filepath.Join(output, "invoice.pdf"+suffix)
				}
				if full {
					args = append(args, "--llm-input=false")
				}
				stdout, stderr, err := f.execute(t.Context(), args...)
				require.NoError(t, err)
				assert.Empty(t, stdout)
				assert.Empty(t, stderr)
				// #nosec G304 -- Reads only this test's private temporary output.
				body, err := os.ReadFile(output)
				require.NoError(t, err)
				if full {
					assert.JSONEq(t, analyzeEnvelope, string(body))
				} else {
					assert.Contains(t, string(body), "mimeType: image/png")
					assert.Contains(t, string(body), "Picture")
					assert.False(t, json.Valid(body))
				}
				// #nosec G304 -- Reads only this test's private temporary report.
				body, err = os.ReadFile(report)
				require.NoError(t, err)
				var summary struct {
					View string `json:"result_view"`
				}
				require.NoError(t, json.Unmarshal(body, &summary))
				want := "llm-input"
				if full {
					want = "full"
				}
				assert.Equal(t, want, summary.View)
			})
		}
	}
}

func TestAnalyzeFuturePreviewInline(t *testing.T) {
	f := newAnalyzeFixture(t)
	f.client.call = func(_ context.Context, request cuclient.AnalyzeRequest) (cuclient.AnalyzeResponse, error) {
		assert.Equal(t, cuclient.AnalysisModeInline, request.Mode)
		return cuclient.AnalyzeResponse{JSON: json.RawMessage(analyzeEnvelope)}, nil
	}
	stdout, _, err := f.execute(t.Context(), "https://example.com/a.pdf", "--inline", "--api-version", "2027-01-01-preview")
	require.NoError(t, err)
	assert.Contains(t, stdout, "Picture")
	assert.EqualValues(t, 1, f.client.calls.Load())
}

func TestAnalyzeResultWriteFailure(t *testing.T) {
	for _, full := range []bool{false, true} {
		t.Run(fmt.Sprint(full), func(t *testing.T) {
			f := newAnalyzeFixture(t)
			root := f.root()
			root.SetOut(failingWriter{})
			root.SetErr(&bytes.Buffer{})
			args := []string{"analyze", "https://example.com/a.pdf"}
			if full {
				args = append(args, "--llm-input=false")
			}
			root.SetArgs(args)
			err := root.ExecuteContext(t.Context())
			require.Error(t, err)
			assert.NotContains(t, err.Error(), "output unavailable")
			assert.Equal(t, f.opens, f.closes)
		})
	}
}
