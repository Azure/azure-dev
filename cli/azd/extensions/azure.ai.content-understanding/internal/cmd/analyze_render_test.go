// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"azure.ai.content-understanding/internal/analysis"
	"azure.ai.content-understanding/internal/cuclient"
	"azure.ai.content-understanding/internal/testutil"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const previewMarkdown = "# Summary\n\n**Important** and *styled*.\n\n" +
	"<table>\n<tr><th>Name</th><th>Value</th></tr>\n<tr><td>Example</td><td>42</td></tr>\n</table>\n"

func previewResponse(t *testing.T, fixture *analyzeFixture) json.RawMessage {
	t.Helper()
	body, err := json.Marshal(map[string]any{
		"status": "Succeeded",
		"result": map[string]any{"contents": []any{map[string]any{"kind": "document", "markdown": previewMarkdown}}},
	})
	require.NoError(t, err)
	fixture.client.call = func(context.Context, cuclient.AnalyzeRequest) (cuclient.AnalyzeResponse, error) {
		return cuclient.AnalyzeResponse{JSON: body}, nil
	}
	return body
}

func TestAnalyzePrettyFlagContract(t *testing.T) {
	root := NewRootCommand()
	command, _, err := root.Find([]string{"analyze"})
	require.NoError(t, err)
	require.NotNil(t, command.Flags().Lookup("pretty"))
	assert.Same(t, command.Flags().Lookup("pretty"), command.Flags().ShorthandLookup("p"))
	setHelpColor(t, false)
	assert.Contains(t, renderHelp(t, command), "-p, --pretty")
	assert.Nil(t, command.Flags().Lookup("render"))

	fixture := newAnalyzeFixture(t)
	_, _, err = fixture.execute(t.Context(), "https://example.com/doc.pdf", "--render")
	require.Error(t, err)
	assert.Zero(t, fixture.opens)
	assert.Zero(t, fixture.client.calls.Load())
}

func TestAnalyzeRenderingIsDefault(t *testing.T) {
	for _, flags := range [][]string{
		nil, {"--pretty=false"}, {"-p=false"}, {"--pretty"}, {"-p"}, {"--pretty=true"}, {"-p=true"},
	} {
		t.Run(strings.Join(flags, " "), func(t *testing.T) {
			fixture := newAnalyzeFixture(t)
			body := previewResponse(t, fixture)
			stdout, stderr, err := fixture.execute(t.Context(), append([]string{"https://example.com/doc.pdf"}, flags...)...)
			require.NoError(t, err)
			assert.Empty(t, stderr)
			assert.NotContains(t, stdout, "\x1b", "injected/redirected output must not contain ANSI")
			assert.EqualValues(t, 1, fixture.client.calls.Load())
			if len(flags) == 0 || !strings.HasSuffix(flags[0], "=false") {
				assert.NotContains(t, stdout, "Preview only")
				assert.Contains(t, stdout, "Important")
				assert.Contains(t, stdout, "Example")
				assert.NotContains(t, stdout, "<table>")
				assert.Contains(t, stdout, "**Important**", "uncolored previews retain ASCII emphasis markers")
			} else {
				var envelope struct{ Result json.RawMessage }
				require.NoError(t, json.Unmarshal(body, &envelope))
				expected, err := analysis.RenderLLM(envelope.Result)
				require.NoError(t, err)
				assert.Equal(t, expected+"\n", stdout)
				assert.Contains(t, stdout, "<table>")
			}
		})
	}
}

func TestAnalyzeRenderRejectsConflictsBeforeAuthentication(t *testing.T) {
	for _, pretty := range []string{"--pretty", "-p", "--pretty=false", "-p=false"} {
		for _, flags := range [][]string{
			{"--output-file", "result.md"}, {"--output-dir", "results"}, {"--output-file="}, {"--output-dir="},
		} {
			t.Run(pretty+" "+strings.Join(flags, " "), func(t *testing.T) {
				fixture := newAnalyzeFixture(t)
				t.Chdir(testutil.TempDir(t))
				args := append([]string{"https://example.com/doc.pdf", pretty}, flags...)
				stdout, stderr, err := fixture.execute(t.Context(), args...)
				require.ErrorContains(t, err, "--pretty")
				assert.Empty(t, stdout)
				assert.Empty(t, stderr)
				assert.Zero(t, fixture.opens)
				assert.Zero(t, fixture.client.calls.Load())
				entries, err := os.ReadDir(".")
				require.NoError(t, err)
				assert.Empty(t, entries)
			})
		}
		if !strings.HasSuffix(pretty, "=false") {
			fixture := newAnalyzeFixture(t)
			first, second := schemaFile(t, "first"), schemaFile(t, "second")
			_, _, err := fixture.execute(t.Context(), first, second, pretty)
			require.ErrorContains(t, err, "batch")
			assert.Zero(t, fixture.opens)
			assert.Zero(t, fixture.client.calls.Load())
		}
	}
}

func TestAnalyzeOriginalFilesAndJSONRemainUnchanged(t *testing.T) {
	for _, format := range []string{"default", "markdown", "json"} {
		fixture := newAnalyzeFixture(t)
		body := previewResponse(t, fixture)
		path := filepath.Join(testutil.TempDir(t), "result.md")
		_, stderr, err := fixture.execute(t.Context(), "https://example.com/doc.pdf",
			"--output-file", path, "--output", format)
		require.NoError(t, err)
		assert.Empty(t, stderr)
		saved, err := os.ReadFile(path) // #nosec G304 -- Fixed filename inside the test's private temporary directory.
		require.NoError(t, err)
		if format == "json" {
			assert.JSONEq(t, string(body), string(saved))
		} else {
			var envelope struct{ Result json.RawMessage }
			require.NoError(t, json.Unmarshal(body, &envelope))
			expected, err := analysis.RenderLLM(envelope.Result)
			require.NoError(t, err)
			assert.Equal(t, expected, string(saved))
		}
		assert.NotContains(t, string(saved), "Preview only")
	}
}

func TestAnalyzeRedirectedStdout(t *testing.T) {
	for _, render := range []bool{false, true} {
		fixture := newAnalyzeFixture(t)
		body := previewResponse(t, fixture)
		file, err := os.CreateTemp(testutil.TempDir(t), "stdout-*.md")
		require.NoError(t, err)
		root := fixture.root()
		var stderr bytes.Buffer
		root.SetOut(file)
		root.SetErr(&stderr)
		flags := []string{"analyze", "https://example.com/doc.pdf"}
		if render {
			flags = append(flags, "--pretty")
		} else {
			flags = append(flags, "--pretty=false")
		}
		root.SetArgs(flags)
		runErr := root.ExecuteContext(t.Context())
		require.NoError(t, file.Close())
		require.NoError(t, runErr)
		result, err := os.ReadFile(file.Name())
		require.NoError(t, err)
		assert.Empty(t, stderr.String())
		assert.NotContains(t, string(result), "\x1b")
		if render {
			assert.NotContains(t, string(result), "Preview only")
			assert.Contains(t, string(result), "Important")
			assert.NotContains(t, string(result), "<table>")
		} else {
			var envelope struct{ Result json.RawMessage }
			require.NoError(t, json.Unmarshal(body, &envelope))
			expected, err := analysis.RenderLLM(envelope.Result)
			require.NoError(t, err)
			assert.Equal(t, expected+"\n", string(result))
		}
	}
}

func TestAnalyzeRenderedPreviewKeepsReportContract(t *testing.T) {
	fixture := newAnalyzeFixture(t)
	previewResponse(t, fixture)
	path := filepath.Join(testutil.TempDir(t), "report.json")
	stdout, stderr, err := fixture.execute(t.Context(), "https://example.com/doc.pdf", "--pretty", "--report-file", path)
	require.NoError(t, err)
	assert.Empty(t, stderr)
	assert.NotContains(t, stdout, "Preview only")
	assert.Contains(t, stdout, "Example")
	report, err := os.ReadFile(path) // #nosec G304 -- Fixed filename inside the test's private temporary directory.
	require.NoError(t, err)
	var parsed map[string]any
	require.NoError(t, json.Unmarshal(report, &parsed))
	assert.Equal(t, "cu-cli/analyze-report/v1", parsed["schema"])
	assert.Equal(t, "llm-input", parsed["result_view"])
	assert.NotContains(t, string(report), "Preview only")
}
