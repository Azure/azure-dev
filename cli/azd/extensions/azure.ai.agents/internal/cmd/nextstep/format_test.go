// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package nextstep

import (
	"bytes"
	"io"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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
			name: "single suggestion renders one line with two-space gap",
			suggestions: []Suggestion{
				{Command: "azd provision", Description: "set up Foundry"},
			},
			want: "\nNext:  azd provision  -- set up Foundry\n",
		},
		{
			name: "two suggestions align on longest command",
			// Longest command "azd ai agent invoke 'hi'" is 24 chars.
			// "azd ai agent show echo" (22) pads with 2 trailing spaces, then the
			// two-space separator + "-- " (commandSeparator = "  -- ") so the gap
			// between "echo" and "--" totals 4 spaces; the second line has no pad
			// so its gap is exactly the 2-space separator.
			suggestions: []Suggestion{
				{Command: "azd ai agent show echo", Description: "verify status"},
				{Command: "azd ai agent invoke 'hi'", Description: "test it"},
			},
			want: "\n" +
				"Next:  azd ai agent show echo    -- verify status\n" +
				"       azd ai agent invoke 'hi'  -- test it\n",
		},
		{
			name: "more than two suggestions are truncated by priority",
			suggestions: []Suggestion{
				{Command: "c", Description: "third", Priority: 30},
				{Command: "a", Description: "first", Priority: 10},
				{Command: "b", Description: "second", Priority: 20},
			},
			want: "\n" +
				"Next:  a  -- first\n" +
				"       b  -- second\n",
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
				"Next:  azd env set BAR <value>  -- supply BAR\n" +
				"       azd deploy               -- when ready\n",
		},
		{
			name: "trailing-only block renders as the single line",
			suggestions: []Suggestion{
				{Command: "azd deploy", Description: "when ready", Priority: 90, Trailing: true},
			},
			want: "\nNext:  azd deploy  -- when ready\n",
		},
		{
			name: "multiple Trailing entries collapse to the lowest-priority one",
			// Defensive: resolvers should emit at most one Trailing entry,
			// but if more are passed in, only the lowest-priority one is
			// rendered to keep the footer single-line.
			suggestions: []Suggestion{
				{Command: "primary", Description: "primary", Priority: 10},
				{Command: "tail-a", Description: "tail a", Priority: 80, Trailing: true},
				{Command: "tail-b", Description: "tail b", Priority: 90, Trailing: true},
			},
			want: "\n" +
				"Next:  primary  -- primary\n" +
				"       tail-a   -- tail a\n",
		},
		{
			name: "stable sort preserves input order on equal priorities",
			suggestions: []Suggestion{
				{Command: "first", Description: "f"},
				{Command: "second", Description: "s"},
			},
			want: "\n" +
				"Next:  first   -- f\n" +
				"       second  -- s\n",
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
