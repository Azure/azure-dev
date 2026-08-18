// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

//go:build live

package cli

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestCLIEvaluatorListBuiltin is the cheapest proof the binary can reach the
// service on its own: no azd project, no config, just a flag.
func TestCLIEvaluatorListBuiltin(t *testing.T) {
	r := requireSuccess(t, run(t, "evaluator", "list", "--builtin", "-o", "json"))

	var builtins []struct {
		Name          string `json:"name"`
		EvaluatorType string `json:"evaluator_type"`
	}
	r.JSON(t, &builtins)
	require.NotEmpty(t, builtins, "the project must expose built-in evaluators")

	for _, b := range builtins {
		require.True(t, strings.HasPrefix(b.Name, "builtin."),
			"--builtin must return only built-ins, got %q", b.Name)
	}

	// The default rendering is a table, not JSON. A script reading stdout
	// without -o json would otherwise silently parse a header row.
	table := requireSuccess(t, run(t, "evaluator", "list", "--builtin"))
	require.Contains(t, table.Stdout, "NAME")
	require.Contains(t, table.Stdout, "VERSION")
}

// TestCLIJSONListsAreBareArrays pins the envelope.
//
// The service wraps listings in {"value":[...]} or {"data":[...]} depending on
// the route. Leaking either would make every consumer special-case the
// command it came from, so the CLI unwraps them, and this is what says so.
func TestCLIJSONListsAreBareArrays(t *testing.T) {
	for _, args := range [][]string{
		{"evaluator", "list", "--builtin", "-o", "json"},
		{"dataset", "list", "-o", "json"},
	} {
		t.Run(strings.Join(args[:2], " "), func(t *testing.T) {
			r := requireSuccess(t, run(t, args...))
			trimmed := strings.TrimSpace(r.Stdout)
			require.True(t, strings.HasPrefix(trimmed, "["),
				"a list must be a bare array, not an envelope; got:\n%s", firstLine(trimmed))

			var out []any
			r.JSON(t, &out)
		})
	}
}

// TestCLIUnknownEvaluatorIsBrief covers the failure a user hits by typo.
//
// The service answers with a long JSON body. Printing it verbatim buries the
// one useful sentence, so the CLI shortens it, and a regression here is the
// kind that only shows up in someone's terminal.
func TestCLIUnknownEvaluatorIsBrief(t *testing.T) {
	r := requireFailure(t, run(t, "evaluator", "show", "azdcli-does-not-exist-9999"))
	require.Less(t, len(r.Combined()), 600,
		"a not-found must stay short, not dump the service body:\n%s", r.Combined())
}

// TestCLIInitNeedsAnAzdProject covers the whole of what `init` can be asked
// through this harness.
//
// init resolves the project over azd's gRPC channel, so it only works when azd
// is hosting the extension. Running the binary directly there is no host, and
// that is exactly the case a user hits by running the command outside a
// project — so what is asserted is the refusal: it must name `azd init` rather
// than surface a transport error. The scaffolding itself is covered by the
// unit tests, which can supply a fake azd client.
func TestCLIInitNeedsAnAzdProject(t *testing.T) {
	dir := t.TempDir()

	r := requireFailure(t, runIn(t, dir, "init",
		"--target", "probe-agent",
		"--judge-model", "gpt-4o-mini",
		"--no-prompt"))

	require.NotContains(t, r.Combined(), "unknown flag",
		"the probe must use init's real flags, or it asserts nothing about projects")
	require.Contains(t, r.Combined(), "azd init",
		"the refusal must name the command that makes a project")
	require.NotContains(t, strings.ToLower(r.Combined()), "grpc",
		"a missing project must not surface as a transport error")

	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.Empty(t, entries,
		"a refused init must leave nothing behind")
}

// TestCLINoPromptFailsInsteadOfHanging is what makes the CLI usable in CI: a
// missing required value must end the process, not wait on a terminal nobody
// is watching.
func TestCLINoPromptFailsInsteadOfHanging(t *testing.T) {
	dir := t.TempDir()
	r := requireFailure(t, runIn(t, dir, "init", "--no-prompt"))
	require.NotEmpty(t, strings.TrimSpace(r.Combined()),
		"--no-prompt must say what it could not resolve")
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
