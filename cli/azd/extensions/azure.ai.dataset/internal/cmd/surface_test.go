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

// The command tree is the spec's `azd ai dataset` table. The groups moved here
// from azure.ai.evaluations, so this is also what says the move was complete.
func TestCommandTreeMatchesTheSpec(t *testing.T) {
	want := []string{
		"create",
		"delete",
		"generate",
		"job",
		"job cancel",
		"job delete",
		"job list",
		"job show",
		"list",
		"show",
		"update",
		"versions",
		"versions list",
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
	forbidden := map[string]string{
		"--out-file": "--output-file",
		"--out-dir":  "--output-dir",
		"--file":     "--from-file",
		"--out":      "--output-file",
		"--dir":      "--output-dir",
	}

	walk(t, NewRootCommand(), nil, func(path string, cmd *cobra.Command) {
		cmd.LocalFlags().VisitAll(func(f *pflag.Flag) {
			if want, bad := forbidden["--"+f.Name]; bad {
				t.Errorf("%s declares --%s; use %s", path, f.Name, want)
			}
		})
	})
}

// `-o json` and `--no-prompt` come from the azd extension SDK's root command,
// so every command inherits them — until one declares a flag by the same name,
// which silently shadows the global.
func TestNoCommandShadowsAGlobalFlag(t *testing.T) {
	global := []string{"output", "no-prompt", "environment", "cwd", "debug"}

	walk(t, NewRootCommand(), nil, func(path string, cmd *cobra.Command) {
		for _, name := range global {
			assert.Nilf(t, cmd.LocalFlags().Lookup(name),
				"%s declares its own --%s, which shadows the global one", path, name)
		}
	})
}

// Every command here reaches the service, so the shared Foundry resolver has to
// be reachable from all of them.
func TestServiceCommandsTakeProjectEndpoint(t *testing.T) {
	walk(t, NewRootCommand(), nil, func(path string, cmd *cobra.Command) {
		if cmd.RunE == nil {
			return
		}
		assert.NotNil(t, cmd.Flags().Lookup("project-endpoint"),
			"%s reaches the service, so it must accept --project-endpoint", path)
	})
}

// The spec says --from "selects one or more" of the sources, so it has to be
// repeatable. Declared as a plain string it would still accept every documented
// single-source invocation and silently keep only the last of a repeated one.
func TestGenerateFromTakesMoreThanOneSource(t *testing.T) {
	flag := find(t, "generate").Flags().Lookup("from")
	require.NotNil(t, flag, "generate must offer --from")

	assert.Equal(t, "stringSlice", flag.Value.Type(),
		"--from selects one or more sources, so it cannot be a single string")

	for _, source := range generateSources {
		assert.Containsf(t, flag.Usage, source,
			"--from accepts %q, so its help has to say so", source)
	}
}

func find(t *testing.T, path string) *cobra.Command {
	t.Helper()
	cmd, _, err := NewRootCommand().Find(strings.Fields(path))
	require.NoError(t, err, "no such command: %s", path)
	require.Equal(t, strings.Fields(path)[len(strings.Fields(path))-1],
		strings.Fields(cmd.Use)[0], "resolved the wrong command for %s", path)
	return cmd
}
