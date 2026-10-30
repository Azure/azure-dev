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
			assert.Contains(t, result, "[HTML table not rendered:")
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
	assert.NotContains(t, result, "[HTML table not rendered:")
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
	// #nosec G101 -- Synthetic credentials exercise URL non-disclosure.
	const unsafeURL = "https://preview-user:preview-password@example.com/doc?sig=private-signature#private-fragment"
	for _, source := range []string{
		"[Document](" + unsafeURL + ")\n",
		unsafeURL + "\n",
		"https&#58;//preview-user:preview-password@example.com/doc?sig=private-signature#private-fragment\n",
		"<table><tr><th>URL</th></tr><tr><td>" + unsafeURL + "</td></tr></table>\n",
		"<table><tr><th>URL</th></tr><tr><td><a href=\"" + unsafeURL + "\">Document</a></td></tr></table>\n",
		"<table><tr><th>URL</th></tr><tr><td><a href=\"https&#58;//preview-user:preview-password@" +
			"example.com/doc?sig=private-signature#private-fragment\">Document</a></td></tr></table>\n",
	} {
		result := renderPreview(t, source, 120)
		for _, secret := range []string{"preview-user", "preview-password", "private-signature", "private-fragment"} {
			assert.NotContains(t, result, secret)
		}
		assert.Contains(t, result, "example.com/doc")
	}
	result := renderPreview(t, "\x1b]52;c;private-clipboard\x07**Safe**\r\x08\n"+
		"<table><tr><th>Name</th></tr><tr><td>&#27;[31mCell</td></tr></table>\n", 80)
	assert.NotContains(t, result, "private-clipboard")
	assert.NotContains(t, result, "\r")
	assert.NotContains(t, result, "\b")
	assert.Contains(t, result, "Safe")
	assert.Contains(t, result, "Cell")
}

func TestSafetyPreservesUnrelatedEscapesAndMarkdownDelimiters(t *testing.T) {
	// #nosec G101 -- Synthetic credentials exercise URL non-disclosure.
	source := "&lt;table&gt; [Link](https://user:password@example.com/path?sig=secret#fragment) &amp;"
	assert.Equal(t, "&lt;table&gt; [Link](https://example.com/path) &amp;", safeText(source))
	assert.Equal(t, "&lt;table&gt; &amp;", safeText("&lt;table&gt; &amp;"))
	assert.Equal(t, "https://example.com/path", safeText("https://example.com/path?"))
	assert.Equal(t, "[redacted URL]", safeText("https://user:password@example.com/%invalid?sig=secret"))
}
