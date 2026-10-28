// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package preview

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"azure.ai.content-understanding/internal/analysis"

	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMetadataUsesDedicatedYAMLBlock(t *testing.T) {
	source := "---\nmimeType: application/pdf\npages: 1\n---\n# Title\n\nBody.\n"
	formatted, err := formatMetadata(source)
	require.NoError(t, err)
	assert.Equal(t, "**Metadata**\n\n```yaml\nmimeType: application/pdf\npages: 1\n```\n\n# Title\n\nBody.\n", formatted)
	for _, color := range []bool{false, true} {
		var output bytes.Buffer
		require.NoError(t, Render(t.Context(), &output, source, 80, color))
		plain := ansi.Strip(output.String())
		assert.Contains(t, plain, "Metadata")
		assert.Contains(t, plain, "mimeType: application/pdf")
		assert.NotContains(t, plain, "## mimeType")
		assert.Contains(t, plain, "Title")
		assert.Contains(t, plain, "Body.")
	}
}

func TestMetadataLeavesMarkdownAndLiteralExamplesAlone(t *testing.T) {
	for _, source := range []string{
		"# Title\n\n---\n\nA horizontal rule.\n",
		"---\nA setext heading\n---\n",
		"---\ntitle: Not LLM metadata\n---\n",
		"---\nmimeType: [invalid YAML\n---\n",
		"---\nmimeType: missing closing delimiter\n",
		"# Examples\n\n```markdown\nExample:\n\n*****\n\n---\nmimeType: text/plain\n---\n```\n",
		"# Examples\n\n~~~markdown\nExample:\n\n*****\n\n---\nmimeType: text/plain\n---\n~~~\n",
		"<!--\nExample:\n\n*****\n\n---\nmimeType: text/plain\n---\n-->\n",
	} {
		formatted, err := formatMetadata(source)
		require.NoError(t, err)
		assert.Equal(t, source, formatted)
	}
}

func TestMetadataAcrossProjectedContentsAndOpaqueValues(t *testing.T) {
	const literalExample = "```markdown\nExample:\n\n*****\n\n---\nmimeType: text/example\n---\n```\n"
	const opaque = "A literal fence:\n````\n<table><tr><th>Not a document table</th></tr></table>\n````"
	const bodyTable = "<table>\n<tr><th>Name</th><th>Count</th></tr>\n" +
		"<tr><td>Document body value</td><td>42</td></tr>\n</table>\n"
	payload, err := json.Marshal(map[string]any{
		"contents": []any{
			map[string]any{
				"kind": "document", "mimeType": "application/pdf",
				"metadata": map[string]any{"note": opaque},
				"markdown": "# First document\n\n" + literalExample + "\n" + bodyTable,
			},
			map[string]any{
				"kind": "audioVisual", "mimeType": "audio/wav", "markdown": "# Second content\n\nTranscript.",
			},
		},
	})
	require.NoError(t, err)
	original, err := analysis.RenderLLM(payload)
	require.NoError(t, err)
	formatted, err := formatMetadata(original)
	require.NoError(t, err)
	assert.Equal(t, 2, strings.Count(formatted, "**Metadata**"))
	assert.Contains(t, formatted, "`````yaml", "opaque backticks cannot close the metadata code block")
	assert.Contains(t, formatted, literalExample, "document code examples must stay literal")
	for _, color := range []bool{false, true} {
		var result bytes.Buffer
		require.NoError(t, Render(t.Context(), &result, original, 120, color))
		plain := ansi.Strip(result.String())
		assert.NotContains(t, plain, "## mimeType")
		assert.Contains(t, plain, "mimeType: application/pdf")
		assert.Contains(t, plain, "mimeType: audio/wav")
		assert.Contains(t, plain, "Not a document table")
		assert.Contains(t, plain, "Document body value")
		assert.NotContains(t, plain, "[HTML table not rendered:")
		assert.Contains(t, plain, "Transcript.")
	}
	assert.True(t, strings.HasPrefix(original, "---\nmimeType: application/pdf\n"))
	assert.Contains(t, original, bodyTable)
	assert.NotContains(t, original, "**Metadata**")
}
