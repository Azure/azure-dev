// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"azure.ai.content-understanding/internal/analysis"
	"azure.ai.content-understanding/internal/testutil"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.yaml.in/yaml/v3"
)

func TestAnalyzeMarkdownUsageInFrontMatter(t *testing.T) {
	for _, format := range []string{"default", "markdown"} {
		for _, mode := range []string{"pretty", "original", "file", "directory"} {
			t.Run(format+"/"+mode, func(t *testing.T) {
				fixture := newAnalyzeFixture(t)
				args := []string{"https://example.com/a.pdf", "-o", format, "--usage"}
				output := filepath.Join(testutil.TempDir(t), "result")
				switch mode {
				case "original":
					args = append(args, "--pretty=false")
				case "file":
					args = append(args, "--output-file", output)
				case "directory":
					args = append(args, "--output-dir", output)
				}
				stdout, stderr, err := fixture.execute(t.Context(), args...)
				require.NoError(t, err)
				assert.Empty(t, stderr)
				content := stdout
				if mode == "file" || mode == "directory" {
					if mode == "directory" {
						entries, err := os.ReadDir(output)
						require.NoError(t, err)
						require.Len(t, entries, 1)
						assert.True(t, strings.HasSuffix(entries[0].Name(), ".result.md"))
						output = filepath.Join(output, entries[0].Name())
					}
					// #nosec G304 -- Output paths are in this test's private directory.
					saved, err := os.ReadFile(output)
					require.NoError(t, err)
					content = string(saved)
					assert.True(t, json.Valid([]byte(stdout)))
				}
				parts := strings.SplitN(content, "---", 3)
				require.Len(t, parts, 3)
				var metadata yaml.Node
				require.NoError(t, yaml.Unmarshal([]byte(parts[1]), &metadata))
				assert.Contains(t, parts[1], "usage:")
				assert.Contains(t, parts[1], "9007199254740993")
				assert.NotContains(t, parts[2], "usage:")
				assert.Contains(t, parts[2], "Picture")
				assert.EqualValues(t, 1, fixture.client.calls.Load())
			})
		}
	}
}

func TestAnalyzeRemovedOutputOptions(t *testing.T) {
	for _, flags := range [][]string{{"--llm-input"}, {"--llm-input=false"}, {"--output", "none"}} {
		fixture := newAnalyzeFixture(t)
		stdout, stderr, err := fixture.execute(t.Context(), append([]string{"https://example.com/a.pdf"}, flags...)...)
		require.Error(t, err)
		assert.Empty(t, stdout)
		assert.Empty(t, stderr)
		assert.Zero(t, fixture.opens)
		assert.Zero(t, fixture.client.calls.Load())
	}
	command, _, err := NewRootCommand().Find([]string{"analyze"})
	require.NoError(t, err)
	assert.Nil(t, command.Flags().Lookup("llm-input"))
	assert.Equal(t, "true", command.Flags().Lookup("pretty").DefValue)
}

func TestAnalyzePrettyDryRun(t *testing.T) {
	for _, format := range []string{"default", "markdown", "json"} {
		fixture := newAnalyzeFixture(t)
		stdout, stderr, err := fixture.execute(t.Context(), "https://example.com/a.pdf", "-o", format,
			"--pretty", "--dry-run", "--usage")
		require.NoError(t, err)
		assert.Empty(t, stderr)
		var plan map[string]any
		require.NoError(t, json.Unmarshal([]byte(stdout), &plan))
		assert.Equal(t, true, plan["dryRun"])
		assert.Zero(t, fixture.client.calls.Load())
	}
}

func TestAnalyzeLocalBatchOutputDestinations(t *testing.T) {
	for _, format := range []string{"default", "markdown", "json"} {
		for _, mode := range []string{"implicit", "beside-inputs", "directory"} {
			t.Run(format+"/"+mode, func(t *testing.T) {
				fixture := newAnalyzeFixture(t)
				body := previewResponse(t, fixture)
				directory := testutil.TempDir(t)
				inputs := []string{filepath.Join(directory, "first.pdf"), filepath.Join(directory, "second.pdf")}
				for _, input := range inputs {
					require.NoError(t, os.WriteFile(input, []byte("input"), 0o600))
				}
				outputDirectory := filepath.Join(directory, "results")
				args := append([]string{"--output", format}, inputs...)
				switch mode {
				case "beside-inputs":
					args = append(args, "--pretty=false")
				case "directory":
					args = append(args, "--output-dir", outputDirectory)
				}
				stdout, stderr, err := fixture.execute(t.Context(), args...)
				if mode == "implicit" {
					require.ErrorContains(t, err, "batch")
					assert.Empty(t, stdout)
					assert.Empty(t, stderr)
					assert.Zero(t, fixture.opens)
					assert.Zero(t, fixture.client.calls.Load())
					entries, err := os.ReadDir(directory)
					require.NoError(t, err)
					assert.Len(t, entries, len(inputs), "invalid invocation must not create results")
					return
				}
				require.NoError(t, err)
				assert.Empty(t, stderr)
				assert.EqualValues(t, len(inputs), fixture.client.calls.Load())
				var records []analysis.Record
				require.NoError(t, json.Unmarshal([]byte(stdout), &records))
				require.Len(t, records, len(inputs))
				suffix := ".result.md"
				if format == "json" {
					suffix = ".result.json"
				}
				var envelope struct{ Result json.RawMessage }
				require.NoError(t, json.Unmarshal(body, &envelope))
				markdown, err := analysis.RenderLLM(envelope.Result)
				require.NoError(t, err)
				for index, input := range inputs {
					output := input + suffix
					if mode == "directory" {
						output = filepath.Join(outputDirectory, filepath.Base(input)) + suffix
					}
					assert.Equal(t, analysis.Record{
						Input: input, Status: "succeeded", Analyzer: fixture.analyzer,
						Output: &output, Usage: json.RawMessage("null"),
					}, records[index])
					// #nosec G304 -- Result paths are in this test's private directory.
					saved, err := os.ReadFile(output)
					require.NoError(t, err)
					if format == "json" {
						assert.JSONEq(t, string(body), string(saved))
					} else {
						assert.Equal(t, markdown, string(saved), "saved results must retain original HTML tables")
					}
				}
			})
		}
	}
}
