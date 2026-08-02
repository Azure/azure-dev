// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

//go:build live

package cli

import (
	"os"
	"path/filepath"
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

// TestCLICodeEvaluatorRoundTrip drives the whole custom evaluator lifecycle
// through the command surface: create from a script, read it back, list its
// versions, then delete it.
func TestCLICodeEvaluatorRoundTrip(t *testing.T) {
	name := uniqueName("azdcli_code")
	script := writeGrader(t, lengthGrader)

	requireSuccess(t, run(t, "evaluator", "create", "--name", name, "--file", script))
	t.Cleanup(func() {
		run(t, "evaluator", "delete", "--name", name, "--version", "1")
	})

	shown := requireSuccess(t, run(t, "evaluator", "show", "--name", name, "-o", "json"))
	var def struct {
		Name       string `json:"name"`
		Definition struct {
			Type     string `json:"type"`
			CodeText string `json:"code_text"`
		} `json:"definition"`
	}
	shown.JSON(t, &def)
	require.Equal(t, "code", def.Definition.Type,
		"a script must register as a code definition")
	require.Contains(t, def.Definition.CodeText, "def grade",
		"the script's source must round-trip in code_text")

	listed := requireSuccess(t, run(t, "evaluator", "list", "--name", name, "-o", "json"))
	var versions []struct {
		Name    string `json:"name"`
		Version string `json:"version"`
	}
	listed.JSON(t, &versions)
	require.NotEmpty(t, versions, "the evaluator must list its own versions")
}

// TestCLIEvaluatorSourcesAreMutuallyExclusive covers the validation a user is
// most likely to trip, and asserts it costs nothing to find out — no version
// is published on the way to the error.
func TestCLIEvaluatorSourcesAreMutuallyExclusive(t *testing.T) {
	script := writeGrader(t, lengthGrader)
	rubric := filepath.Join(t.TempDir(), "rubric.json")
	require.NoError(t, os.WriteFile(rubric,
		[]byte(`{"dimensions":[{"id":"tone","description":"polite","weight":5}]}`), 0o600))

	both := requireFailure(t, run(t, "evaluator", "create",
		"--name", uniqueName("azdcli_both"), "--file", script, "--rubric", rubric))
	require.Contains(t, strings.ToLower(both.Combined()), "rubric",
		"the error must name the flags in conflict")

	neither := requireFailure(t, run(t, "evaluator", "create",
		"--name", uniqueName("azdcli_neither")))
	require.NotEmpty(t, strings.TrimSpace(neither.Combined()),
		"refusing without a source must explain itself")
}

// TestCLIGraderIsValidatedBeforePublishing asserts the check that saves a user
// from a late failure: a script with no top-level grade() is refused locally,
// because the executor would otherwise accept the publish and fail the run.
func TestCLIGraderIsValidatedBeforePublishing(t *testing.T) {
	script := writeGrader(t, `class AnswerLengthEvaluator:
    def __call__(self, **kwargs):
        return {"result": 1.0}
`)

	r := requireFailure(t, run(t, "evaluator", "create",
		"--name", uniqueName("azdcli_noglade"), "--file", script))
	require.Contains(t, strings.ToLower(r.Combined()), "grade",
		"the refusal must name the function the executor looks for")
}

// TestCLIUnknownEvaluatorIsBrief covers the failure a user hits by typo.
//
// The service answers with a long JSON body. Printing it verbatim buries the
// one useful sentence, so the CLI shortens it, and a regression here is the
// kind that only shows up in someone's terminal.
func TestCLIUnknownEvaluatorIsBrief(t *testing.T) {
	r := requireFailure(t, run(t, "evaluator", "show", "--name", "azdcli-does-not-exist-9999"))
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
