// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"io"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The README's command table is the first thing a reader copies from, and it
// had drifted badly: it advertised `evaluator upload`, `evaluator builtins` and
// an `azd ai eval results` group, none of which exist, while omitting eval
// CRUD, generation jobs, version listings, run deletion and `run output`.
// Following it produced unknown-command errors.
//
// TestCommandTreeMatchesTheSpec already pins the tree. This pins the document
// to the same tree, because a table nobody checks is a table that drifts again.
func TestReadmeCommandTableNamesOnlyRealCommands(t *testing.T) {
	table := readmeCommandTable(t)
	require.NotEmpty(t, table, "the command table was not found in README.md")

	real := map[string]bool{}
	walk(t, NewRootCommand(), nil, func(path string, _ *cobra.Command) {
		real[path] = true
	})

	for _, cmd := range table {
		assert.True(t, real[cmd],
			"README names %q, which is not a registered command", cmd)
	}
}

// The other direction: a command the extension grew but the README never
// mentions is a feature readers cannot find.
func TestReadmeCommandTableCoversEveryGroup(t *testing.T) {
	table := map[string]bool{}
	for _, cmd := range readmeCommandTable(t) {
		table[cmd] = true
	}

	walk(t, NewRootCommand(), nil, func(path string, c *cobra.Command) {
		// Groups exist to hold their children; the table lists them as rows
		// rather than as entries, and `versions` is named through its `list`.
		if c.HasSubCommands() {
			return
		}
		assert.True(t, table[path],
			"%q is a command the README's table never mentions", path)
	})
}

// readmeCommandTable reads the rows of the "## Commands" table and returns the
// full command paths they name.
//
// A row is `| `azd ai eval run` | `start` . `list` . ... |`, so the group comes
// from the first cell and each command from the second. Argument placeholders
// are dropped, since the tree is keyed on the verb.
func readmeCommandTable(t *testing.T) []string {
	t.Helper()

	root, err := os.OpenRoot("../..")
	require.NoError(t, err, "opening the extension directory")
	defer root.Close()

	f, err := root.Open("README.md")
	require.NoError(t, err, "opening README.md")
	defer f.Close()

	body, err := io.ReadAll(f)
	require.NoError(t, err)

	code := regexp.MustCompile("`([^`]+)`")
	var out []string
	inTable := false

	for line := range strings.SplitSeq(string(body), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "| Group ") {
			inTable = true
			continue
		}
		if inTable && !strings.HasPrefix(line, "|") {
			break
		}
		if !inTable || strings.HasPrefix(line, "|---") {
			continue
		}

		cells := strings.Split(strings.Trim(line, "|"), "|")
		if len(cells) < 2 {
			continue
		}
		group := groupPath(code.FindStringSubmatch(cells[0]))
		for _, m := range code.FindAllStringSubmatch(cells[1], -1) {
			out = append(out, strings.TrimSpace(group+" "+verbOnly(m[1])))
		}
	}
	return out
}

// groupPath turns the first cell into the path prefix the tree uses, which is
// everything after the `azd ai eval` the tree does not include.
func groupPath(match []string) string {
	if len(match) < 2 {
		return ""
	}
	return strings.TrimSpace(strings.TrimPrefix(match[1], "azd ai eval"))
}

// verbOnly drops the argument placeholder the table shows for readability.
func verbOnly(verb string) string {
	verb = strings.TrimSpace(verb)
	if i := strings.IndexAny(verb, "<["); i >= 0 {
		verb = strings.TrimSpace(verb[:i])
	}
	return verb
}
