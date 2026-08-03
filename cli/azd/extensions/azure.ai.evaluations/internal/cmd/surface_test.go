// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The command surface is a contract with the spec and with the sibling Foundry
// extensions, and it is the part of this tool users type from memory. Nothing
// was checking it: the flag that writes results to a file was `--out-file`
// while the spec, Scenario 4, and `azd ai skill download` all say
// `--output-file`, and it took reading the two documents side by side to see.
//
// These tests walk the built tree, so a command or flag that is renamed,
// dropped, or quietly added has to be acknowledged here.

// walk visits every command in the tree, skipping the ones azd contributes.
func walk(t *testing.T, cmd *cobra.Command, path []string, visit func(string, *cobra.Command)) {
	t.Helper()
	for _, child := range cmd.Commands() {
		name := strings.Fields(child.Use)[0]
		switch name {
		case "help", "completion", "listen", "metadata":
			continue
		}
		full := append(append([]string{}, path...), name)
		visit(strings.Join(full, " "), child)
		walk(t, child, full, visit)
	}
}

// commandTree is every command the extension exposes, and is the surface the
// spec's command table describes.
func TestCommandTreeMatchesTheSpec(t *testing.T) {
	want := []string{
		"dataset",
		"dataset create",
		"dataset delete",
		"dataset generate",
		"dataset list",
		"dataset show",
		"dataset update",
		"dataset versions",
		"dataset versions list",
		"delete",
		"evaluator",
		"evaluator create",
		"evaluator delete",
		"evaluator generate",
		"evaluator list",
		"evaluator show",
		"evaluator update",
		"evaluator versions",
		"evaluator versions list",
		"init",
		"job",
		"job cancel",
		"job list",
		"job show",
		"list",
		"run",
		"run cancel",
		"run delete",
		"run list",
		"run output",
		"run output export",
		"run output list",
		"run output show",
		"run show",
		"run start",
		"show",
	}

	var got []string
	walk(t, NewRootCommand(), nil, func(path string, _ *cobra.Command) {
		got = append(got, path)
	})

	assert.ElementsMatch(t, want, got,
		"the command tree changed; update the spec's command table with it")
}

// Flag names are shared vocabulary across the Foundry extensions. A command
// that invents its own spelling for something the others already name is the
// kind of difference nobody notices until a user types the one they learned
// somewhere else.
func TestFlagVocabularyIsShared(t *testing.T) {
	// Meaning → the one spelling for it, from the spec's vocabulary table.
	// A command that means one of these must use exactly this name, and the
	// near-misses are listed so a rename back is caught rather than accepted.
	forbidden := map[string]string{
		"--out-file":     "--output-file",
		"--out-dir":      "--output-dir",
		"--file":         "--from-file",
		"--rubric":       "--from-file",
		"--judge-model":  "--generation-model, declared per evaluator instead",
		"--from-traces":  "deferred to M2",
		"--response-id":  "deferred to M2",
		"--no-target":    "deferred to M2",
		"--out":          "--output-file",
		"--dir":          "--output-dir",
		"--baseline":     "deferred to M2",
		"--cron":         "deferred to M2",
		"--folder":       "deferred to M2",
		"--init-params":  "deferred to M2",
		"--data-schema":  "deferred to M2",
		"--metrics":      "deferred to M2",
		"--trace-window": "deferred to M2",
		"--max-traces":   "deferred to M2",
		"--max-turns":    "deferred to M2",
	}

	walk(t, NewRootCommand(), nil, func(path string, cmd *cobra.Command) {
		cmd.LocalFlags().VisitAll(func(f *pflag.Flag) {
			if want, bad := forbidden["--"+f.Name]; bad {
				t.Errorf("%s declares --%s; use %s", path, f.Name, want)
			}
		})
	})
}

// The two commands that write a file have to agree on what that flag is
// called, and it has to be the name the sibling extensions use.
func TestOutputFileFlagIsSpelledTheSharedWay(t *testing.T) {
	for _, path := range []string{"run output list", "run output export"} {
		cmd := find(t, path)
		require.NotNil(t, cmd.Flags().Lookup("output-file"),
			"%s must write to --output-file, the name `azd ai skill download` uses", path)
		assert.Nil(t, cmd.Flags().Lookup("out-file"),
			"%s must not keep the old spelling alongside the shared one", path)
	}
}

// `init` is the one command with a documented flag table, so it is pinned
// whole: an extra flag there is a promise the spec does not make, and a
// missing one is a promise it does.
func TestInitFlagsMatchTheSpec(t *testing.T) {
	cmd := find(t, "init")

	var got []string
	cmd.LocalFlags().VisitAll(func(f *pflag.Flag) {
		if f.Name != "help" {
			got = append(got, "--"+f.Name)
		}
	})

	assert.ElementsMatch(t, []string{
		"--name", "--target", "--dataset", "--evaluator",
		"--generation-model", "--output-dir", "--force",
	}, got, "init's flags are a table in the spec; change both together")
}

// `init` makes no service calls, so it must not offer the flag that says where
// to make them.
func TestInitTakesNoProjectEndpoint(t *testing.T) {
	assert.Nil(t, find(t, "init").Flags().Lookup("project-endpoint"),
		"init is offline; a project endpoint would imply otherwise")
}

// Every command that does reach the service accepts it, because the shared
// Foundry resolver is how a project is named without an azd environment.
func TestServiceCommandsTakeProjectEndpoint(t *testing.T) {
	groups := map[string]bool{
		"dataset": true, "evaluator": true, "run": true,
		"job": true, "run output": true,
	}

	walk(t, NewRootCommand(), nil, func(path string, cmd *cobra.Command) {
		if cmd.RunE == nil || path == "init" {
			return
		}
		if groups[path] {
			return
		}
		assert.NotNil(t, cmd.Flags().Lookup("project-endpoint"),
			"%s reaches the service, so it must accept --project-endpoint", path)
	})
}

// find resolves a command path, failing the test when it does not exist.
func find(t *testing.T, path string) *cobra.Command {
	t.Helper()
	cmd, _, err := NewRootCommand().Find(strings.Fields(path))
	require.NoError(t, err, "no such command: %s", path)
	require.Equal(t, strings.Fields(path)[len(strings.Fields(path))-1],
		strings.Fields(cmd.Use)[0], "resolved the wrong command for %s", path)
	return cmd
}
