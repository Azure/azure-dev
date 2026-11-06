// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package preview

import (
	"bytes"
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func renderPreview(t *testing.T, source string, width int) string {
	t.Helper()
	var result bytes.Buffer
	require.NoError(t, Render(t.Context(), &result, source, width, false))
	require.NotContains(t, result.String(), "\x1b")
	return result.String()
}

func TestMarkdownStylesAndHTMLTable(t *testing.T) {
	source := "# Results\n\n**Bold**, *italic*, ***both***.\n\n" +
		"<table>\n<caption>Totals</caption>\n<tr><th>Name</th><th>Value</th></tr>\n" +
		"<tr><td>A &amp; B</td><td>**literal**</td></tr>\n</table>\n\nAfter table.\n"
	result := renderPreview(t, source, 120)
	assert.Contains(t, result, "Bold")
	assert.Contains(t, result, "italic")
	assert.Contains(t, result, "both")
	assert.NotContains(t, result, "***both***")
	assert.Contains(t, result, "A & B")
	assert.Contains(t, result, "**literal**")
	assert.NotContains(t, result, "<td>")
	assert.NotContains(t, result, "Row 1")
	assert.Less(t, strings.Index(result, "Totals"), strings.Index(result, "A & B"))
	assert.Contains(t, result, "After table.")

	var colored bytes.Buffer
	require.NoError(t, Render(t.Context(), &colored, "**Bold**\n", 80, true))
	assert.Contains(t, colored.String(), "\x1b")
	assert.Contains(t, ansi.Strip(colored.String()), "Bold")
}

func TestPrettyPreservesDocumentStructure(t *testing.T) {
	t.Setenv("NO_COLOR", "")
	const metadata = "---\nmimeType: application/pdf\npages: 1\n---\n"
	const comment = "<!-- PageNumber=\"1\" -->"
	source := metadata + "# Document title\n\nFirst source line.\nSecond source line.\n\n" + comment + "\n"
	for _, color := range []bool{false, true} {
		var result bytes.Buffer
		require.NoError(t, Render(t.Context(), &result, source, 120, color))
		plain := ansi.Strip(result.String())
		assert.Contains(t, plain, metadata)
		assert.Contains(t, plain, comment)
		assert.Contains(t, plain, "# Document title")
		assert.Contains(t, plain, "Document title")
		assert.Regexp(t, `First source line\.\s*\n\s*Second source line\.`, plain)
		assert.NotContains(t, plain, "Preview only")
		assert.NotContains(t, plain, "URLs omit")
		if color {
			assert.Contains(t, result.String(), "\x1b[90;2m---")
			assert.Contains(t, result.String(), "\x1b[90;2m"+comment)
		}
	}
	commentWithTable := "<!-- PageFooter=\"<table> is an example\" -->"
	plain := renderPreview(t, commentWithTable+"\n", 120)
	assert.Contains(t, plain, commentWithTable)
	assert.NotContains(t, plain, "HTML table not rendered:")
}

func TestH1UsesTheOtherHeadingStyles(t *testing.T) {
	t.Setenv("NO_COLOR", "")
	const source = "# First\n\n## Second\n\n### Third\n\n#### Fourth\n"
	for _, color := range []bool{false, true} {
		var result bytes.Buffer
		require.NoError(t, Render(t.Context(), &result, source, 120, color))
		plain := ansi.Strip(result.String())
		for _, heading := range []string{"# First", "## Second", "### Third", "#### Fourth"} {
			assert.Contains(t, plain, heading)
		}
		if color {
			for line := range strings.SplitSeq(result.String(), "\n") {
				for _, title := range []string{"First", "Second", "Third", "Fourth"} {
					if strings.Contains(line, title) {
						assert.Contains(t, line, "\x1b[38;5;39;1m", "all headings use the shared heading color")
						assert.NotContains(t, line, "\x1b[48;", "H1 has no dedicated background")
					}
				}
			}
		}
	}
}

func TestPrettyLayoutNoticesAndRowAnnotations(t *testing.T) {
	t.Setenv("NO_COLOR", "")
	const table = "<table><tr><th>Name</th><th>Value</th></tr>" +
		"<tr><td>First</td><td>RECORD0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZ</td></tr>" +
		"<tr><td>Second</td><td>RECORD9876543210ABCDEFGHIJKLMNOPQRSTUVWXYZ</td></tr></table>"
	for _, width := range []int{20, 160} {
		var result bytes.Buffer
		require.NoError(t, Render(t.Context(), &result, table, width, true))
		plain := ansi.Strip(result.String())
		if width == 20 {
			assert.Contains(t, result.String(), "\x1b[33m<!-- Table exceeds terminal width;")
			assert.Contains(t, plain, "<!-- Row 1 -->")
			assert.Contains(t, plain, "<!-- Row 2 -->")
			assert.Regexp(t, "\x1b\\[90;2m[^\\n]*<!-- Row 1 -->", result.String())
		} else {
			assert.NotContains(t, plain, "Table exceeds terminal width")
			assert.NotContains(t, plain, "<!-- Row")
		}
	}
	const merged = "<table><tr><th>Name</th></tr><tr><td colspan=\"2\">Merged</td></tr></table>"
	var result bytes.Buffer
	require.NoError(t, Render(t.Context(), &result, merged, 120, true))
	assert.Contains(t, result.String(), "\x1b[33m<!-- HTML table not rendered:")
	assert.Contains(t, ansi.Strip(result.String()), merged)
}

func TestWideTableSelectsCardsAndKeepsValues(t *testing.T) {
	const identifier = "RECORD0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZ"
	const link = "https://example.com/reports/2026/quarterly/network/event-log/details"
	source := "<table>\n<tr><th>Identifier</th><th>URL</th></tr>\n<tr><td>" + identifier +
		"</td><td>" + link + "</td></tr>\n</table>\n"
	for _, width := range []int{20, 40, 80, 120} {
		t.Run(strconv.Itoa(width), func(t *testing.T) {
			result := renderPreview(t, source, width)
			assert.Contains(t, result, identifier)
			assert.Contains(t, result, link)
			if width < 120 {
				assert.Contains(t, result, "Row 1")
				assert.Contains(t, result, "Identifier:")
				assert.Contains(t, result, "URL:")
			} else {
				assert.NotContains(t, result, "Row 1")
			}
		})
	}
}

func TestReferenceLinksAcrossHTMLTables(t *testing.T) {
	const table = "<table><tr><th>Name</th></tr><tr><td>Example</td></tr></table>\n\n"
	for _, sample := range []struct {
		name, source, destination string
	}{
		{"definition-after", "See [the report][report].\n\n" + table +
			"[report]: https://example.com/report\n", "https://example.com/report"},
		{"definition-before", "[report]: https://example.com/report\n\n" + table +
			"See [the report][report].\n", "https://example.com/report"},
		{"first-definition-wins", "[report]: https://example.com/first\n\n" + table +
			"See [the report][report].\n\n[report]: https://example.com/second\n", "https://example.com/first"},
		{"multiline", "See [the report][report].\n\n" + table +
			"[report]:\n  <https://example.com/report>\n  \"Report title\"\n", "https://example.com/report"},
		{"multiple-tables", "See [the report][report].\n\n" + table + table +
			"[report]: https://example.com/report\n", "https://example.com/report"},
		{"collapsed-reference", "See [report][].\n\n" + table +
			"[report]: https://example.com/report\n", "https://example.com/report"},
		{"shortcut-reference", "See [report].\n\n" + table +
			"[report]: https://example.com/report\n", "https://example.com/report"},
		{"image-reference", "![Report image][report]\n\n" + table +
			"[report]: https://example.com/report\n", "https://example.com/report"},
		{"nested-definition", "> [report]: https://example.com/report\n\n" + table +
			"See [the report][report].\n", "https://example.com/report"},
		{"escaped-label", "See [the report][report\\]].\n\n" + table +
			"[report\\]]: <https://example.com/report>\n", "https://example.com/report"},
		{"fenced-definition-is-literal", "```markdown\n[report]: https://example.com/literal\n```\n\n" +
			"See [the report][report].\n\n" + table + "[report]: https://example.com/report\n",
			"https://example.com/report"},
		{"unrendered-table", "See [the report][report].\n\n" +
			"<table><tr><th colspan=\"2\">Name</th></tr><tr><td>Example</td></tr></table>\n\n" +
			"[report]: https://example.com/report\n", "https://example.com/report"},
	} {
		t.Run(sample.name, func(t *testing.T) {
			for _, color := range []bool{false, true} {
				var result bytes.Buffer
				require.NoError(t, Render(t.Context(), &result, sample.source, 120, color))
				plain := ansi.Strip(result.String())
				assert.Contains(t, plain, sample.destination)
				assert.NotContains(t, plain, "[the report][report]")
				assert.NotContains(t, plain, "https://example.com/second")
				assert.Contains(t, plain, "Example")
			}
		})
	}
}

func TestPrettyPrintTableColorsPreserveContentAndLayout(t *testing.T) {
	t.Setenv("NO_COLOR", "")
	const source = "<table><caption>Totals</caption><tr><th>Name</th><th>Value</th></tr>" +
		"<tr><td>Example</td><td>RECORD0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZ</td></tr></table>\n"
	for _, width := range []int{20, 120} {
		t.Run(strconv.Itoa(width), func(t *testing.T) {
			plain := renderPreview(t, source, width)
			var colored bytes.Buffer
			require.NoError(t, Render(t.Context(), &colored, source, width, true))
			assert.Contains(t, colored.String(), "\x1b[36mTotals")
			assert.Equal(t, plain, ansi.Strip(colored.String()))
			if width == 20 {
				assert.Contains(t, colored.String(), "Row 1")
			}
			t.Setenv("NO_COLOR", "1")
			var disabled bytes.Buffer
			require.NoError(t, Render(t.Context(), &disabled, source, width, true))
			assert.Equal(t, plain, disabled.String())
		})
	}
}

func TestUnsupportedTablesPreserveSource(t *testing.T) {
	for _, source := range []string{
		"<table><tr><th>A</th><th>B</th></tr><tr><td colspan=\"2\">Merged</td></tr></table>",
		"<table><tr><th>A</th></tr><tr><td><strong>Rich</strong></td></tr></table>",
		"<table><tr><th>A</th></tr><tr><td></td></tr></table>",
		"<table><tr><th>A</th><th>A</th></tr><tr><td>1</td><td>2</td></tr></table>",
		"<table><tr><th>A</th></tr><tr><td><table><tr><td>Nested</td></tr></table></td></tr></table>",
		"<table><tr><td>Not a heading</td></tr></table>",
	} {
		t.Run(source, func(t *testing.T) {
			result := renderPreview(t, source+"\n", 80)
			assert.Contains(t, result, "<!-- HTML table not rendered:")
			assert.Contains(t, result, source)
		})
	}
}

func TestCodeFenceAndNativeMarkdownTablesStayWithGlamour(t *testing.T) {
	source := "```html\n<table><tr><th>A</th></tr><tr><td>B</td></tr></table>\n```\n\n" +
		"| Name | Value |\n| --- | --- |\n| Native | 42 |\n"
	result := renderPreview(t, source, 120)
	assert.Contains(t, result, "<table>")
	assert.Contains(t, result, "Native")
	assert.Contains(t, result, "42")
	assert.NotContains(t, result, "HTML table not rendered:")
	assert.NotContains(t, result, "Row 1")
}

func TestCardsRetainOriginalRowHeadings(t *testing.T) {
	source := "<table><tr><th>Row</th><th>_Row</th><th>Details</th></tr>" +
		"<tr><td>Original</td><td>Another</td><td>RECORD0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZ</td></tr></table>\n"
	result := renderPreview(t, source, 20)
	for _, value := range []string{"Row 1", "Row:", "_Row:", "Original", "Another"} {
		assert.Contains(t, result, value)
	}
}

func TestPinnedCardsUnicodeAlignmentCharacterization(t *testing.T) {
	source := "<table><tr><th>城市</th><th>Description</th></tr>" +
		"<tr><td>台北</td><td>RECORD0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZ</td></tr></table>\n"
	result := renderPreview(t, source, 20)
	columns := map[string]int{}
	for line := range strings.SplitSeq(result, "\n") {
		for _, value := range []string{"台北", "RECORD0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZ"} {
			if prefix, _, ok := strings.Cut(line, value); ok {
				columns[value] = ansi.StringWidth(prefix)
			}
		}
	}
	require.Len(t, columns, 2)
	assert.NotEqual(t, columns["台北"], columns["RECORD0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZ"],
		"azd v1.37.0 cards still pad labels by byte length; this test records, not fixes, the upstream limitation")
}

type failingWriter struct{ err error }

func (w failingWriter) Write([]byte) (int, error) { return 0, w.err }

func TestPreviewErrorsAndCancellation(t *testing.T) {
	errWrite := errors.New("writer unavailable")
	require.ErrorIs(t, Render(t.Context(), failingWriter{errWrite}, "Result", 80, false), errWrite)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	var result bytes.Buffer
	require.ErrorIs(t, Render(ctx, &result, "Result", 80, false), context.Canceled)
	assert.Empty(t, result.String())
	require.ErrorContains(t, Render(t.Context(), &result, "Result", 0, false), "width")
	assert.Empty(t, result.String())
}

func TestPreviewSafetyForMarkdownAndHTML(t *testing.T) {
	result := renderPreview(t, "\x1b]52;c;private-clipboard\x07**Safe**\r\x08\n"+
		"<table><tr><th>Name</th></tr><tr><td>&#27;[31mCell</td></tr></table>\n", 80)
	assert.NotContains(t, result, "private-clipboard")
	assert.NotContains(t, result, "\r")
	assert.NotContains(t, result, "\b")
	assert.Contains(t, result, "Safe")
	assert.Contains(t, result, "Cell")
}

func TestSafetyPreservesUnrelatedEscapesAndMarkdownDelimiters(t *testing.T) {
	for _, source := range []string{
		"&lt;table&gt; &amp;",
		"A &amp; B",
		"A &amp;amp; B",
		"# Title\n\n**Important** and *emphasis*.",
		"中文、日本語、한글 😀",
		"https://example.com/doc?page=2#section",
		"https://example.com/doc?",
		"[Document](https://example.com/doc?page=2#section)",
	} {
		t.Run(source, func(t *testing.T) {
			assert.Equal(t, source, safeText(source))
		})
	}
}

func TestSafetyNormalizesNewlinesAndRemovesControls(t *testing.T) {
	for _, sample := range []struct {
		name, source, expected string
	}{
		{"newlines-and-tabs", "First\r\n\tSecond\nThird", "First\n\tSecond\nThird"},
		{"raw-controls", "A\x00\x08\rB", "AB"},
		{"ansi-color", "\x1b[31mRed\x1b[0m", "Red"},
		{"decimal-entity", "A&#27;B", "AB"},
		{"hex-entity", "A&#x1b;B", "AB"},
		{"encoded-newline-and-tab", "A&#10;&#9;B", "A&#10;&#9;B"},
		{"bidi-controls", "中文\u202eABC\u202c\u2066日本語\u2069", "中文ABC日本語"},
		{"encoded-bidi", "A&#x202e;B&#8236;", "AB"},
	} {
		t.Run(sample.name, func(t *testing.T) {
			actual := safeText(sample.source)
			assert.Equal(t, sample.expected, actual)
			assert.Equal(t, actual, safeText(actual), "cleaning is stable across repeated calls")
		})
	}
}

func TestPrettyDecodesEntitiesOnce(t *testing.T) {
	for _, sample := range []struct {
		source, expected string
	}{
		{"A &amp; B\n", "A & B"},
		{"A &amp;amp; B\n", "A &amp; B"},
		{"&lt;table&gt;\n", "<table>"},
		{"<table><tr><th>Text</th></tr><tr><td>A &amp;amp; B</td></tr></table>\n", "A &amp; B"},
	} {
		result := renderPreview(t, sample.source, 120)
		assert.Contains(t, result, sample.expected)
		assert.NotContains(t, result, "HTML table not rendered:")
	}
}

func TestPrettyPreservesOrdinaryURLComponents(t *testing.T) {
	t.Setenv("NO_COLOR", "")
	const destination = "https://example.com/doc?page=2&view=full#section"
	for _, sample := range []struct {
		name, source, expected, notice string
		width                          int
	}{
		{"plain", destination + "\n", destination, "", 200},
		{"link", "[Document](" + destination + ")\n", destination, "", 200},
		{"encoded", "https&#58;//example.com/doc?page=2&amp;view=full#section\n", destination, "", 200},
		{"empty-query", "https://example.com/doc?\n", "https://example.com/doc?", "", 200},
		{"table", "<table><tr><th>URL</th></tr><tr><td>" + destination + "</td></tr></table>\n",
			destination, "", 200},
		{"cards", "<table><tr><th>URL</th></tr><tr><td>" + destination + "</td></tr></table>\n",
			destination, "Table exceeds terminal width", 20},
		{"fallback", "<table><tr><th>URL</th></tr><tr><td colspan=\"2\">" + destination + "</td></tr></table>\n",
			destination, "HTML table not rendered", 200},
	} {
		t.Run(sample.name, func(t *testing.T) {
			for _, color := range []bool{false, true} {
				var result bytes.Buffer
				require.NoError(t, Render(t.Context(), &result, sample.source, sample.width, color))
				plain := ansi.Strip(result.String())
				assert.Contains(t, plain, sample.expected)
				if sample.notice != "" {
					assert.Contains(t, plain, sample.notice)
				}
				if !color {
					assert.NotContains(t, result.String(), "\x1b")
				}
			}
		})
	}
}
