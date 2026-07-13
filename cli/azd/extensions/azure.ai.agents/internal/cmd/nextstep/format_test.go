// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package nextstep

import (
	"bytes"
	"io"
	"os"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/fatih/color"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestMain suppresses ANSI color so the rendered-block golden assertions
// below compare plain text. highlightCommand routes azd commands through
// output.WithHighLightFormat, which only emits escape sequences when color
// is enabled; forcing NoColor keeps the goldens deterministic regardless of
// the host TTY / NO_COLOR state. TestFormatNext_HighlightsOnlyAzdCommands
// re-enables color locally to exercise the highlight path.
func TestMain(m *testing.M) {
	color.NoColor = true
	os.Exit(m.Run())
}

func TestPrintNext(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		suggestions []Suggestion
		want        string
	}{
		{
			name:        "empty input produces no output",
			suggestions: nil,
			want:        "",
		},
		{
			name: "single suggestion stacks command over description",
			suggestions: []Suggestion{
				{Command: "azd provision", Description: "set up Foundry"},
			},
			want: "\nNext:\n  azd provision\n  set up Foundry\n",
		},
		{
			name: "two suggestions are separated by a blank line",
			suggestions: []Suggestion{
				{Command: "azd ai agent show echo", Description: "verify status"},
				{Command: "azd ai agent invoke 'hi'", Description: "test it"},
			},
			want: "\n" +
				"Next:\n" +
				"  azd ai agent show echo\n" +
				"  verify status\n" +
				"\n" +
				"  azd ai agent invoke 'hi'\n" +
				"  test it\n",
		},
		{
			name: "more than two suggestions are truncated by priority",
			suggestions: []Suggestion{
				{Command: "c", Description: "third", Priority: 30},
				{Command: "a", Description: "first", Priority: 10},
				{Command: "b", Description: "second", Priority: 20},
			},
			want: "\n" +
				"Next:\n" +
				"  a\n" +
				"  first\n" +
				"\n" +
				"  b\n" +
				"  second\n",
		},
		{
			name: "trailing suggestion survives truncation when primaries fill the block",
			// Three primary suggestions would normally fill maxRendered (2)
			// and drop the highest-priority trailing entry. The renderer
			// must instead reserve the last slot for the Trailing footer so
			// resolver-emitted follow-up nudges (e.g., `azd deploy`) are
			// always visible.
			suggestions: []Suggestion{
				{Command: "azd env set BAR <value>", Description: "supply BAR", Priority: 20},
				{Command: "azd env set FOO <value>", Description: "supply FOO", Priority: 21},
				{Command: "azd env set BAZ <value>", Description: "supply BAZ", Priority: 22},
				{Command: "azd deploy", Description: "when ready", Priority: 90, Trailing: true},
			},
			want: "\n" +
				"Next:\n" +
				"  azd env set BAR <value>\n" +
				"  supply BAR\n" +
				"\n" +
				"  azd deploy\n" +
				"  when ready\n",
		},
		{
			name: "trailing-only block renders as a single suggestion",
			suggestions: []Suggestion{
				{Command: "azd deploy", Description: "when ready", Priority: 90, Trailing: true},
			},
			want: "\nNext:\n  azd deploy\n  when ready\n",
		},
		{
			name: "multiple Trailing entries collapse to the highest-priority one",
			// Defensive: resolvers should emit at most one Trailing entry,
			// but if more are passed in, only the highest-Priority one
			// is rendered — the most-deferred footer wins, protecting
			// the intended `azd deploy` slot from accidental
			// lower-Priority Trailing flags.
			suggestions: []Suggestion{
				{Command: "primary", Description: "primary", Priority: 10},
				{Command: "tail-a", Description: "tail a", Priority: 80, Trailing: true},
				{Command: "tail-b", Description: "tail b", Priority: 90, Trailing: true},
			},
			want: "\n" +
				"Next:\n" +
				"  primary\n" +
				"  primary\n" +
				"\n" +
				"  tail-b\n" +
				"  tail b\n",
		},
		{
			name: "stable sort preserves input order on equal priorities",
			suggestions: []Suggestion{
				{Command: "first", Description: "f"},
				{Command: "second", Description: "s"},
			},
			want: "\n" +
				"Next:\n" +
				"  first\n" +
				"  f\n" +
				"\n" +
				"  second\n" +
				"  s\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var buf bytes.Buffer
			require.NoError(t, PrintNext(&buf, tt.suggestions))
			assert.Equal(t, tt.want, buf.String())
		})
	}
}

// failingWriter returns an error on first Write; used to verify PrintNext
// propagates I/O errors from the underlying writer.
type failingWriter struct{}

func (failingWriter) Write(_ []byte) (int, error) {
	return 0, io.ErrUnexpectedEOF
}

func TestPrintNext_PropagatesWriteError(t *testing.T) {
	t.Parallel()

	err := PrintNext(failingWriter{}, []Suggestion{{Command: "x", Description: "y"}})
	require.ErrorIs(t, err, io.ErrUnexpectedEOF)
}

func TestPrintNext_EmptyInputSkipsWrite(t *testing.T) {
	t.Parallel()

	// failingWriter would error if Write were called; nil suggestions
	// must short-circuit before any write.
	require.NoError(t, PrintNext(failingWriter{}, nil))
}

func TestPrintAllNext(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		suggestions []Suggestion
		want        string
	}{
		{
			name:        "empty input produces no output",
			suggestions: nil,
			want:        "",
		},
		{
			name: "single suggestion renders identically to PrintNext",
			suggestions: []Suggestion{
				{Command: "azd provision", Description: "set up Foundry"},
			},
			want: "\nNext:\n  azd provision\n  set up Foundry\n",
		},
		{
			name: "G1 regression repro: placeholder + manual var + trailing deploy all render (no cap)",
			// This is the toolbox-sample state that motivated commit 2194327e8.
			// PrintNext (capped at 2 with trailing reservation) would render
			// only [placeholder, deploy] and drop the env-set line, leaving
			// the user thinking they only need to fix the placeholder before
			// deploying. PrintAllNext must surface all three.
			suggestions: []Suggestion{
				{
					Command:     "edit azure.yaml: replace {{TOOLBOX_ENDPOINT}} with the actual value",
					Description: "azure.yaml has unresolved manifest placeholders",
					Priority:    5,
				},
				{
					Command:     "azd env set TOOLBOX_WEB_SEARCH_TOOLS_MCP_ENDPOINT <value>",
					Description: "supply the azure.yaml variable",
					Priority:    6,
				},
				{
					Command:     "azd deploy",
					Description: "when ready to deploy to Azure",
					Priority:    90,
					Trailing:    true,
				},
			},
			want: "\n" +
				"Next:\n" +
				"  edit azure.yaml: replace {{TOOLBOX_ENDPOINT}} with the actual value\n" +
				"  azure.yaml has unresolved manifest placeholders\n" +
				"\n" +
				"  azd env set TOOLBOX_WEB_SEARCH_TOOLS_MCP_ENDPOINT <value>\n" +
				"  supply the azure.yaml variable\n" +
				"\n" +
				"  azd deploy\n" +
				"  when ready to deploy to Azure\n",
		},
		{
			name: "renders well beyond maxRendered (3 placeholders + 3 manual vars + trailing)",
			// Worst-case shape from ResolveAfterInit when both
			// maxFixupLines caps are saturated.
			suggestions: []Suggestion{
				{Command: "edit azure.yaml: replace {{A}} with the actual value", Description: "p1", Priority: 5},
				{Command: "edit azure.yaml: replace {{B}} with the actual value", Description: "p1", Priority: 6},
				{Command: "edit azure.yaml: replace {{C}} with the actual value", Description: "p1", Priority: 7},
				{Command: "azd env set FOO <value>", Description: "p2", Priority: 8},
				{Command: "azd env set BAR <value>", Description: "p2", Priority: 9},
				{Command: "azd env set BAZ <value>", Description: "p2", Priority: 10},
				{Command: "azd deploy", Description: "p3", Priority: 90, Trailing: true},
			},
			want: "\n" +
				"Next:\n" +
				"  edit azure.yaml: replace {{A}} with the actual value\n" +
				"  p1\n" +
				"\n" +
				"  edit azure.yaml: replace {{B}} with the actual value\n" +
				"  p1\n" +
				"\n" +
				"  edit azure.yaml: replace {{C}} with the actual value\n" +
				"  p1\n" +
				"\n" +
				"  azd env set FOO <value>\n" +
				"  p2\n" +
				"\n" +
				"  azd env set BAR <value>\n" +
				"  p2\n" +
				"\n" +
				"  azd env set BAZ <value>\n" +
				"  p2\n" +
				"\n" +
				"  azd deploy\n" +
				"  p3\n",
		},
		{
			name: "trailing entry still rendered last regardless of input order",
			suggestions: []Suggestion{
				{Command: "azd deploy", Description: "when ready", Priority: 90, Trailing: true},
				{Command: "first", Description: "f", Priority: 5},
				{Command: "second", Description: "s", Priority: 6},
			},
			want: "\n" +
				"Next:\n" +
				"  first\n" +
				"  f\n" +
				"\n" +
				"  second\n" +
				"  s\n" +
				"\n" +
				"  azd deploy\n" +
				"  when ready\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var buf bytes.Buffer
			require.NoError(t, PrintAllNext(&buf, tt.suggestions))
			assert.Equal(t, tt.want, buf.String())
		})
	}
}

func TestPrintAllNext_PropagatesWriteError(t *testing.T) {
	t.Parallel()

	err := PrintAllNext(failingWriter{}, []Suggestion{{Command: "x", Description: "y"}})
	require.ErrorIs(t, err, io.ErrUnexpectedEOF)
}

func TestPrintAllNext_EmptyInputSkipsWrite(t *testing.T) {
	t.Parallel()

	require.NoError(t, PrintAllNext(failingWriter{}, nil))
}

func TestFormatNextForNote(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		suggestions []Suggestion
		want        string
	}{
		{
			name:        "empty input produces empty string",
			suggestions: nil,
			want:        "",
		},
		{
			name: "single suggestion has a leading newline and no trailing newline",
			suggestions: []Suggestion{
				{Command: "azd ai agent invoke 'hello'", Description: "send a test request", Priority: 10},
			},
			want: "\nNext:\n  azd ai agent invoke 'hello'\n  send a test request",
		},
		{
			name: "multi-line block stacks each suggestion with a blank separator",
			suggestions: []Suggestion{
				{Command: "azd ai agent show", Description: "verify deployment", Priority: 10},
				{Command: "azd ai agent invoke 'hi'", Description: "send a request", Priority: 11},
			},
			want: "\n" +
				"Next:\n" +
				"  azd ai agent show\n" +
				"  verify deployment\n" +
				"\n" +
				"  azd ai agent invoke 'hi'\n" +
				"  send a request",
		},
		{
			name: "uncapped — third suggestion is preserved (unlike PrintNext)",
			suggestions: []Suggestion{
				{Command: "azd ai agent show", Description: "verify deployment", Priority: 10},
				{Command: "azd ai agent invoke 'hi'", Description: "send a request", Priority: 11},
				{Command: "see ./agent/README.md", Description: "more sample requests", Priority: 12},
			},
			want: "\n" +
				"Next:\n" +
				"  azd ai agent show\n" +
				"  verify deployment\n" +
				"\n" +
				"  azd ai agent invoke 'hi'\n" +
				"  send a request\n" +
				"\n" +
				"  see ./agent/README.md\n" +
				"  more sample requests",
		},
		{
			name: "trailing entry surfaces even when not the lowest priority",
			suggestions: []Suggestion{
				{Command: "azd ai agent show", Description: "verify deployment", Priority: 10},
				{Command: "azd deploy", Description: "redeploy after changes", Priority: 90, Trailing: true},
				{Command: "azd ai agent invoke 'hi'", Description: "send a request", Priority: 11},
			},
			want: "\n" +
				"Next:\n" +
				"  azd ai agent show\n" +
				"  verify deployment\n" +
				"\n" +
				"  azd ai agent invoke 'hi'\n" +
				"  send a request\n" +
				"\n" +
				"  azd deploy\n" +
				"  redeploy after changes",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := FormatNextForNote(tc.suggestions)
			assert.Equal(t, tc.want, got)
		})
	}
}

// TestFormatNextForNote_HostArtifactAlignment verifies the block renders
// correctly when embedded as an artifact note by core azd. The renderer
// (cli/azd/pkg/project/artifact.go) appends the note via "\n%s  %s" with the
// caller indent applied to the first line only; the deploy path renders
// artifacts at the zero indent. FormatNextForNote's leading newline turns
// that first, indent-only line into a blank separator, after which the
// "Next:" header sits in the same column as the endpoint bullet and each
// suggestion steps in by bodyIndent.
func TestFormatNextForNote_HostArtifactAlignment(t *testing.T) {
	t.Parallel()

	note := FormatNextForNote([]Suggestion{
		{Command: "azd ai agent show", Description: "verify deployment", Priority: 10},
		{Command: "azd ai agent invoke 'hi'", Description: "send a request", Priority: 11},
	})

	// Simulate core azd's render at the deploy (zero) indent:
	// "{indent}- label: location\n{indent}  {note}\n".
	const callerIndent = ""
	rendered := callerIndent + "- endpoint: https://example/agents/foo/endpoint\n" +
		callerIndent + "  " + note + "\n"

	want := "- endpoint: https://example/agents/foo/endpoint\n" +
		"  \n" +
		"Next:\n" +
		"  azd ai agent show\n" +
		"  verify deployment\n" +
		"\n" +
		"  azd ai agent invoke 'hi'\n" +
		"  send a request\n"
	assert.Equal(t, want, rendered)
}

// TestFormatNext_HighlightsOnlyAzdCommands verifies that runnable azd
// commands are rendered in the highlight color while non-command pointers
// ("see ..." / "edit azure.yaml: ...") and descriptions stay plain.
func TestFormatNext_HighlightsOnlyAzdCommands(t *testing.T) {
	// Not parallel: toggles the process-global color.NoColor. Go runs
	// non-parallel tests to completion before parallel tests resume, so
	// restoring NoColor here keeps the plain-text goldens deterministic.
	prev := color.NoColor
	color.NoColor = false
	defer func() { color.NoColor = prev }()

	suggestions := []Suggestion{
		{Command: "azd ai agent show echo", Description: "verify it's running", Priority: 10},
		{Command: "see src/echo/README.md", Description: "find the sample-specific payload", Priority: 11},
		{Command: "azd ai agent invoke echo '<payload>'", Description: "test the deployment", Priority: 12},
	}

	got := FormatNextForNote(suggestions)

	// azd commands are wrapped in the highlight color.
	assert.Contains(t, got, bodyIndent+output.WithHighLightFormat("%s", "azd ai agent show echo")+"\n")
	assert.Contains(t, got, bodyIndent+output.WithHighLightFormat("%s", "azd ai agent invoke echo '<payload>'")+"\n")
	// Non-command pointers stay plain (no escape sequences).
	assert.Contains(t, got, bodyIndent+"see src/echo/README.md\n")
	assert.NotContains(t, got, output.WithHighLightFormat("%s", "see src/echo/README.md"))
	// Descriptions are never highlighted.
	assert.Contains(t, got, bodyIndent+"verify it's running")
}
