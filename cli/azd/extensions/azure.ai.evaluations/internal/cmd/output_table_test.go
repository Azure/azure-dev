// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A list view is uppercase headers over a rule, per the spec's output
// conventions and the sibling extension it cites. The rule is what separates
// the header from the data at a glance, and every `list` command was printing
// the header straight onto the first row.
func TestEmitTableWritesTheRule(t *testing.T) {
	var buf bytes.Buffer
	require.NoError(t, emitTable(&buf,
		[]string{"NAME", "VERSION"},
		[][]string{{"support-regression", "3"}, {"nightly", "1"}}))

	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	require.Len(t, lines, 4, "a header, its rule, and one line per row")

	assert.Contains(t, lines[0], "NAME")
	assert.Contains(t, lines[0], "VERSION")

	// Dashes as wide as the header they sit under, which is what makes the
	// rule line up once tabwriter has padded the columns.
	assert.Contains(t, lines[1], strings.Repeat("-", len("NAME")))
	assert.Contains(t, lines[1], strings.Repeat("-", len("VERSION")))
	assert.Empty(t, strings.Trim(lines[1], "- "),
		"the rule carries nothing but dashes and padding")

	assert.Contains(t, lines[2], "support-regression")
	assert.Contains(t, lines[3], "nightly")
}

// The columns line up: the rule is padded to the same widths as the header, so
// a wide value in the first row does not leave the rule short.
func TestEmitTableRuleAlignsWithTheHeader(t *testing.T) {
	var buf bytes.Buffer
	require.NoError(t, emitTable(&buf,
		[]string{"NAME", "STATUS"},
		[][]string{{"a-very-much-longer-value-than-the-header", "completed"}}))

	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	require.Len(t, lines, 3)

	// tabwriter pads every line in a column to the same width, so the header
	// and its rule start their second column at the same offset.
	assert.Equal(t,
		strings.Index(lines[0], "STATUS"),
		strings.Index(lines[1], "------"),
		"the rule has to sit under the header it belongs to")
}

// A listing with nothing in it still prints the header and rule: a caller
// seeing no output cannot tell an empty list from a command that failed to
// render.
func TestEmitTableWithNoRows(t *testing.T) {
	var buf bytes.Buffer
	require.NoError(t, emitTable(&buf, []string{"NAME", "VERSION"}, nil))

	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	assert.Len(t, lines, 2)
}
