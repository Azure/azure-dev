// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
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

// Messages that tell a user what to run next have to name a command that
// exists.
//
// In the extension these commands moved from, three suggestions pointed at
// `azd ai dataset ...` while that namespace was served by nobody, and the
// check there matched on the wrong prefix so none of them failed. This
// extension's namespace is `ai.dataset`; anything it suggests under
// `azd ai dataset` has to resolve here, and a suggestion under another
// namespace is one it cannot make.
func TestSuggestedCommandsExist(t *testing.T) {
	pattern := regexp.MustCompile("azd ai ([a-z][a-z0-9-]*(?: [a-z][a-z0-9-]*)*)")

	err := filepath.WalkDir("../..", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, line := range strings.Split(string(body), "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), "//") {
				continue
			}
			for _, m := range pattern.FindAllStringSubmatch(line, -1) {
				words := strings.Fields(m[1])
				if len(words) == 0 {
					continue
				}
				// A suggestion pointing at a sibling extension is that
				// extension's contract, not this one's, and cannot be resolved
				// from here. Listed rather than wildcarded so a typo in a
				// namespace still fails.
				if siblingNamespaces[words[0]] {
					continue
				}
				assert.Equalf(t, "dataset", words[0],
					"%s suggests `azd ai %s`, which is neither this extension's "+
						"namespace nor a sibling it knows about", path, m[1])
				words = words[1:]

				// Trim trailing prose: "job show" is a command, "job show and
				// then" is a sentence that begins with one.
				for len(words) > 0 {
					if resolved, _, e := NewRootCommand().Find(words); e == nil {
						if strings.Fields(resolved.Use)[0] == words[len(words)-1] {
							break
						}
					}
					words = words[:len(words)-1]
				}
				assert.NotEmptyf(t, words,
					"%s suggests `azd ai %s`, which is not a command", path, m[1])
			}
		}
		return nil
	})
	require.NoError(t, err)
}

// siblingNamespaces are the other Foundry extensions this one points users at.
var siblingNamespaces = map[string]bool{
	"eval":    true, // registering a generated dataset in an eval configuration
	"project": true, // `azd ai project set` owns the shared endpoint context
}

func find(t *testing.T, path string) *cobra.Command {
	t.Helper()
	cmd, _, err := NewRootCommand().Find(strings.Fields(path))
	require.NoError(t, err, "no such command: %s", path)
	require.Equal(t, strings.Fields(path)[len(strings.Fields(path))-1],
		strings.Fields(cmd.Use)[0], "resolved the wrong command for %s", path)
	return cmd
}
