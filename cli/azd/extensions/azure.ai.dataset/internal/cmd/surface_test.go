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

// The command tree is the spec's `azd ai dataset` table. The CRUD groups moved
// here from azure.ai.evaluations; `generate` deliberately did not, because it
// writes the `datasets:` entry in evals/eval.yaml, which that extension owns.
func TestCommandTreeMatchesTheSpec(t *testing.T) {
	want := []string{
		"create",
		"delete",
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

// Generation stays with `azure.ai.evaluations`, so nothing here may grow a
// `--from`: a second generate would be a second place for the catalog write to
// go missing.
func TestNoGenerationCommandLandsHere(t *testing.T) {
	walk(t, NewRootCommand(), nil, func(path string, cmd *cobra.Command) {
		assert.Nilf(t, cmd.Flags().Lookup("from"),
			"%s offers --from; generation belongs to azure.ai.evaluations", path)
		assert.NotEqualf(t, "generate", cmd.Name(),
			"%s is a generation command; it belongs to azure.ai.evaluations", path)
	})
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
		body, err := os.ReadFile(path) //nolint:gosec // walking this package's own source
		if err != nil {
			return err
		}
		for line := range strings.SplitSeq(string(body), "\n") {
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

// A suggestion is only as good as its flags. TestSuggestedCommandsExist stops
// at the command, so `azd ai dataset create <name> --file <path>` passed it
// while `--file` did not exist: the real flag is `--from-file`, and a reader
// following the advice got "unknown flag". Checking the command without its
// flags checks the easy half.
func TestSuggestedFlagsExist(t *testing.T) {
	// A suggestion is inside backticks, so the flags belonging to it end where
	// the quoting does and prose afterwards is not mistaken for one.
	quoted := regexp.MustCompile("`azd ai ([^`]*)`")

	err := filepath.WalkDir("../..", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		body, err := os.ReadFile(path) //nolint:gosec // walking this package's own source
		if err != nil {
			return err
		}
		for line := range strings.SplitSeq(string(body), "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), "//") {
				continue
			}
			for _, m := range quoted.FindAllStringSubmatch(line, -1) {
				fields := strings.Fields(m[1])
				if len(fields) == 0 || fields[0] != "dataset" {
					continue // a sibling's contract, checked by the test above
				}

				// The command is the leading run of plain words; a placeholder
				// like <name> or a %q verb ends it.
				var words, flags []string
				for _, f := range fields[1:] {
					switch {
					case strings.HasPrefix(f, "--"):
						flags = append(flags, strings.TrimPrefix(strings.SplitN(f, "=", 2)[0], "--"))
					case len(flags) == 0 && regexp.MustCompile(`^[a-z][a-z0-9-]*$`).MatchString(f):
						words = append(words, f)
					}
				}
				if len(flags) == 0 {
					continue
				}

				// NewRootCommand() is the `dataset` command itself, so the
				// namespace word is not part of the path to look up.
				resolved, _, e := NewRootCommand().Find(words)
				if !assert.NoErrorf(t, e, "%s suggests `azd ai %s`, which is not a command", path, m[1]) {
					continue
				}
				for _, name := range flags {
					assert.NotNilf(t, resolved.Flags().Lookup(name),
						"%s suggests `azd ai %s`, but `%s` has no --%s flag",
						path, m[1], resolved.CommandPath(), name)
				}
			}
		}
		return nil
	})
	require.NoError(t, err)
}

// A message pointing at `azd ai eval dataset ...` is almost always the copy// these commands came from rather than a deliberate cross-extension pointer.
// `dataset` is this extension's own namespace, so telling a user to run the
// eval extension's version of a command it serves itself sends them somewhere
// they may not have installed.
//
// Nothing here may suggest one any more: `generate` was the only command with a
// reason to, and it stayed with azure.ai.evaluations.
func TestNoStaleEvalDatasetSuggestions(t *testing.T) {
	allowed := map[string]bool{}

	err := filepath.WalkDir("../..", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		body, err := os.ReadFile(path) //nolint:gosec // walking this package's own source
		if err != nil {
			return err
		}
		pattern := regexp.MustCompile(`azd ai eval dataset [a-z][a-z0-9-]*`)
		for i, line := range strings.Split(string(body), "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), "//") {
				continue
			}
			for _, m := range pattern.FindAllString(line, -1) {
				assert.Truef(t, allowed[m],
					"%s:%d suggests `%s`; this extension serves that command as "+
						"`azd ai dataset ...`", path, i+1, m)
			}
		}
		return nil
	})
	require.NoError(t, err)
}

// siblingNamespaces are the other Foundry extensions this one points users at.
var siblingNamespaces = map[string]bool{
	"project": true, // `azd ai project set` owns the shared endpoint context
}
