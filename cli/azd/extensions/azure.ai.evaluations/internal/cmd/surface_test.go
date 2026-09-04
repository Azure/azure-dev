// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"azureaieval/internal/project"

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
		case "help", "completion", "listen", "metadata", "version":
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
		"dataset list",
		"dataset show",
		"dataset update",
		"dataset versions",
		"dataset versions list",
		"create",
		"delete",
		"evaluator",
		"evaluator create",
		"evaluator delete",
		"evaluator list",
		"evaluator show",
		"evaluator update",
		"evaluator versions",
		"evaluator versions list",
		// One generate for both artifacts, and one job group for both
		// collections, selected by --dataset / --evaluator.
		"generate",
		"job",
		"job cancel",
		"job delete",
		"job list",
		"job show",
		"init",
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

// M1 promises `-o json` and `--no-prompt` throughout. Both come from the azd
// extension SDK's root command, so every command inherits them — until one
// declares its own flag by the same name, which silently shadows the global
// and leaves that one command unable to answer in JSON or to run unattended.
func TestNoCommandShadowsAGlobalFlag(t *testing.T) {
	global := []string{"output", "no-prompt", "environment", "cwd", "debug"}

	walk(t, NewRootCommand(), nil, func(path string, cmd *cobra.Command) {
		for _, name := range global {
			assert.Nilf(t, cmd.LocalFlags().Lookup(name),
				"%s declares its own --%s, which shadows the global one", path, name)
		}
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
		"--name", "--target", "--source", "--dataset", "--max-traces",
		"--evaluator", "--judge-model", "--path", "--force",
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
// --eval takes "the name of the eval declared in the configuration", which
// means the command has to be able to find that configuration. The only other
// way it can is a path `init` recorded in the azd environment, which a
// --project-endpoint caller does not have -- so a configuration outside ./evals
// could be started and then not listed, shown or cancelled.
func TestEveryCommandTakingAnEvalNameCanBeToldWhereTheConfigIs(t *testing.T) {
	var checked int
	walk(t, NewRootCommand(), nil, func(path string, cmd *cobra.Command) {
		if cmd.Flags().Lookup("eval") == nil {
			return
		}
		checked++
		assert.NotNilf(t, cmd.Flags().Lookup("path"),
			"%s resolves a declared eval name, so it must accept --path", path)
	})
	assert.NotZero(t, checked, "no command took --eval, so this checked nothing")
}

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

// The spec says --from "selects one or more of the four sources", so it has to
// be repeatable. Declared as a plain string it would still accept every
// documented single-source invocation and silently keep only the last of a
// repeated one, which is the kind of difference no example in the spec shows.
func TestGenerateFromTakesMoreThanOneSource(t *testing.T) {
	flag := find(t, "generate").Flags().Lookup("from")
	require.NotNil(t, flag, "generate must offer --from")

	assert.Equal(t, "stringSlice", flag.Value.Type(),
		"--from selects one or more sources, so it cannot be a single string")
}

// `--from` names sources; the set it offers is the set generate can build
// from, and the help has to list exactly that set.
func TestGenerateFromListsEverySource(t *testing.T) {
	usage := find(t, "generate").Flags().Lookup("from").Usage

	for _, source := range project.GenerateSources {
		assert.Containsf(t, usage, source,
			"--from offers %q, so its help has to say so", source)
	}
}

// `file` is recognized so that asking for it earns the remedy rather than a
// list of the others, but generate never builds from one. Advertising it would
// offer a value the same command guarantees to refuse.
func TestGenerateDoesNotOfferTheSourceItAlwaysRefuses(t *testing.T) {
	usage := find(t, "generate").Flags().Lookup("from").Usage

	assert.NotContains(t, usage, project.GenerateFromFile,
		"--from file is refused, so the help must not list it")
	assert.NoError(t, project.ValidateGenerateSource(project.GenerateFromFile),
		"it is still recognized, so the refusal can name what to do instead")
}

// Waiting is already generate's default, so --wait changes nothing. A script
// that spells out what it wants should not be refused for saying so.
func TestGenerateTakesTheWaitFlagItDocuments(t *testing.T) {
	flags := find(t, "generate").Flags()

	for _, name := range []string{"wait", "no-wait"} {
		assert.NotNilf(t, flags.Lookup(name), "generate must offer --%s", name)
	}
}

// One command now generates both artifacts, but --from and --max-samples shape
// the dataset only. The help has to say so, or they read as applying to the
// rubric as well.
func TestGenerateSaysWhichFlagsAreDatasetOnly(t *testing.T) {
	flags := find(t, "generate").Flags()

	for _, name := range []string{"from", "max-samples"} {
		flag := flags.Lookup(name)
		require.NotNilf(t, flag, "generate must offer --%s", name)
		assert.Containsf(t, flag.Usage, "Dataset only",
			"--%s shapes the dataset only, so its help has to say so", name)
	}
}

// The selector narrows generation; omitting both is the zero-to-first-eval
// path, so neither flag may be required.
func TestGenerateSelectorIsOptional(t *testing.T) {
	cmd := find(t, "generate")

	for _, name := range []string{"dataset", "evaluator"} {
		flag := cmd.Flags().Lookup(name)
		require.NotNilf(t, flag, "generate must offer --%s", name)
		assert.Equal(t, "false", flag.DefValue,
			"--%s is off by default, which is what generates both", name)
	}
}

// The spec's run table says which commands carry which flag. Where it says
// "every", that is checkable; where it names two commands, a third carrying the
// flag is a promise the spec does not make and a missing one is a promise it
// does.
//
// This pins placement, not the whole flag list: unlike init's, the run table is
// headed "Flag | Commands | Default" and documents defaults rather than
// enumerating every flag.
func TestRunFlagsSitWhereTheSpecSaysTheyDo(t *testing.T) {
	// Flag → exactly the run commands that may declare it. nil means every
	// run command that does something.
	placement := map[string][]string{
		"eval":    nil,
		"dataset": {"run start"},
		"fail-on": {"run start", "run show"},
		"wait":    {"run start", "run show"},
		"format":  {"run output export"},
	}

	// Every run command that actually runs, which is what "every run command"
	// means — the bare groups take no flags.
	var runCommands []string
	walk(t, NewRootCommand(), nil, func(path string, cmd *cobra.Command) {
		if strings.HasPrefix(path, "run") && cmd.RunE != nil {
			runCommands = append(runCommands, path)
		}
	})
	require.NotEmpty(t, runCommands)

	for flag, allowed := range placement {
		if allowed == nil {
			allowed = runCommands
		}
		for _, path := range runCommands {
			has := find(t, path).Flags().Lookup(flag) != nil
			want := slices.Contains(allowed, path)

			switch {
			case want && !has:
				t.Errorf("%s must accept --%s; the spec's run table says so", path, flag)
			case !want && has:
				t.Errorf("%s declares --%s, which the spec gives only to %s",
					path, flag, strings.Join(allowed, ", "))
			}
		}
	}
}

// `--eval` is how a run command finds the eval, and the spec gives it to every
// one of them. Losing it from a single command makes that command unusable in a
// project with more than one eval.
func TestEveryRunCommandTakesEval(t *testing.T) {
	walk(t, NewRootCommand(), nil, func(path string, cmd *cobra.Command) {
		if !strings.HasPrefix(path, "run") || cmd.RunE == nil {
			return
		}
		assert.NotNilf(t, cmd.Flags().Lookup("eval"),
			"%s must accept --eval, which the spec gives to every run command", path)
	})
}

// The tagged suites drive the binary by writing flags as strings, so a flag
// that is renamed or removed still compiles there and only fails when someone
// has the credentials to run them.
//
// That is not hypothetical. Removing `--eval-id` and the generation spec file
// left 28 uses of `--eval-id` and two `--config` tests behind in tests/cli,
// every one of which would have failed at the first live run — under `live` and
// `hero` tags that `go test ./...` never builds. This checks them from the
// default suite, where a rename is caught by the person doing the renaming.
func TestTaggedSuitesNameFlagsThatExist(t *testing.T) {
	// Every flag any command declares, plus the globals azd contributes.
	known := map[string]bool{
		"output": true, "no-prompt": true, "environment": true,
		"cwd": true, "debug": true, "help": true,
	}
	walk(t, NewRootCommand(), nil, func(_ string, cmd *cobra.Command) {
		cmd.Flags().VisitAll(func(f *pflag.Flag) { known[f.Name] = true })
	})

	// Only string literals, which is how a test spells a flag it passes to the
	// binary. Prose in a comment is not a flag.
	literal := regexp.MustCompile(`"--([a-z][a-z0-9-]*)"`)

	err := filepath.WalkDir("../../tests", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") {
			return nil
		}
		body, err := os.ReadFile(path) //nolint:gosec // walking this package's own source
		if err != nil {
			return err
		}
		for i, line := range strings.Split(string(body), "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), "//") {
				continue
			}
			for _, m := range literal.FindAllStringSubmatch(line, -1) {
				assert.Truef(t, known[m[1]],
					"%s:%d passes --%s, which no command declares", path, i+1, m[1])
			}
		}
		return nil
	})
	require.NoError(t, err)
}

// A suggestion that names a flag has to name one the suggested command takes.
//
// `run start --no-wait` closed with "Reattach with: azd ai eval run show <id>
// --eval-id <eval>" for the whole life of the branch that removed --eval-id.
// The command resolved, so the suggestion check passed; the flag did not exist,
// so the one line a user is told to paste was the one guaranteed to fail.
func TestSuggestedFlagsExist(t *testing.T) {
	// `azd ai eval <command path> ... --flag`, with the flag anywhere after it.
	suggestion := regexp.MustCompile(`azd ai eval ((?:[a-z][a-z0-9-]*\s+)+)([^"'\n]*)`)
	flagName := regexp.MustCompile(`--([a-z][a-z0-9-]*)`)

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
		for i, line := range strings.Split(string(body), "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), "//") {
				continue
			}
			for _, m := range suggestion.FindAllStringSubmatch(line, -1) {
				flags := flagName.FindAllStringSubmatch(m[2], -1)
				if len(flags) == 0 {
					continue
				}
				// Longest command prefix that resolves; the rest is arguments.
				words := strings.Fields(m[1])
				for len(words) > 0 {
					if resolved, _, e := NewRootCommand().Find(words); e == nil &&
						strings.Fields(resolved.Use)[0] == words[len(words)-1] {
						break
					}
					words = words[:len(words)-1]
				}
				if len(words) == 0 {
					continue // the command itself is checked above
				}
				cmd, _, _ := NewRootCommand().Find(words)
				for _, f := range flags {
					assert.NotNilf(t, cmd.Flags().Lookup(f[1]),
						"%s:%d suggests `azd ai eval %s --%s`, which that command does not accept",
						path, i+1, strings.Join(words, " "), f[1])
				}
			}
		}
		return nil
	})
	require.NoError(t, err)
}

// siblingNamespaces are the other Foundry extensions this one points users at.
// Listed rather than wildcarded so a typo in a namespace still fails.
var siblingNamespaces = map[string]bool{
	"project": true, // `azd ai project set` owns the shared endpoint context
	"dataset": true, // where the dataset commands go once they move
	"agent":   true,
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

// Messages that tell a user what to run next have to name a command that
// exists.
//
// Rebuilding the surface left `run start --no-wait` closing with "Check
// progress with: azd ai eval results show", a command that had been renamed
// out of existence — so the one instruction printed at the moment a user needs
// it was the one thing guaranteed to fail. Nothing catches that: the string
// compiles, the command that prints it succeeds, and only someone following
// the advice finds out.
// This extension's namespace is `ai.eval`, so every command it can suggest
// begins `azd ai eval`. Anchoring on that prefix is what caught the renamed
// command above — and anchoring only on it is what let three suggestions
// through pointing at `azd ai dataset`, a namespace no installed extension
// serves. So the prefix checked is `azd ai`, and anything under it that is not
// this extension's own is a command nobody can run.
func TestSuggestedCommandsExist(t *testing.T) {
	root := "../.."
	pattern := regexp.MustCompile("azd ai ([a-z][a-z0-9-]*(?: [a-z][a-z0-9-]*)*)")

	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
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
			// Comments explain the surface; only what reaches a terminal has
			// to resolve.
			if strings.HasPrefix(strings.TrimSpace(line), "//") {
				continue
			}
			for _, m := range pattern.FindAllStringSubmatch(line, -1) {
				words := strings.Fields(m[1])
				if len(words) == 0 {
					continue
				}

				// A suggestion under a sibling's namespace is that extension's
				// contract and cannot be resolved from here. Only the ones this
				// extension actually points at are allowed, so a typo still
				// fails rather than passing as "probably somebody else's".
				if siblingNamespaces[words[0]] {
					continue
				}

				// `ai.eval` is this extension's namespace, so it is the only
				// other thing under `azd ai` that can resolve.
				if words[0] != "eval" {
					t.Errorf("%s suggests `azd ai %s`, which is neither this "+
						"extension's namespace nor a sibling it knows about", path, m[1])
					continue
				}
				words = words[1:]

				// Trim trailing prose: "run start" is a command, "run start
				// and summarize" is a sentence that begins with one.
				for len(words) > 0 {
					if _, _, err := NewRootCommand().Find(words); err == nil {
						resolved, _, _ := NewRootCommand().Find(words)
						if strings.Fields(resolved.Use)[0] == words[len(words)-1] {
							break
						}
					}
					words = words[:len(words)-1]
				}
				assert.NotEmpty(t, words,
					"%s suggests `azd ai %s`, which is not a command", path, m[1])
			}
		}
		return nil
	})
	require.NoError(t, err)
}

// Every flag a message names has to exist on some command.
//
// TestSuggestedFlagsExist only looks inside a quoted `azd ai eval ...` command,
// so a message that names a flag on its own escapes it. One did:
// DatasetHasUnregisteredEdits told the reader to pass `--eval-id <id>`, a flag
// removed long before, and the check above saw no command to attach it to.
func TestBareFlagsInMessagesExist(t *testing.T) {
	flagRef := regexp.MustCompile(`--([a-z][a-z0-9-]{1,})`)

	known := map[string]bool{}
	root := NewRootCommand()
	root.PersistentFlags().VisitAll(func(f *pflag.Flag) { known[f.Name] = true })
	walk(t, root, nil, func(_ string, c *cobra.Command) {
		c.Flags().VisitAll(func(f *pflag.Flag) { known[f.Name] = true })
	})
	// azd's own, which messages legitimately name.
	for _, global := range []string{"cwd", "debug", "environment", "no-prompt", "output", "help"} {
		known[global] = true
	}

	body, err := os.ReadFile(filepath.Join("..", "messages", "messages.go"))
	require.NoError(t, err)

	for i, line := range strings.Split(string(body), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "//") {
			continue
		}
		for _, m := range flagRef.FindAllStringSubmatch(line, -1) {
			assert.Truef(t, known[m[1]],
				"messages.go:%d names --%s, which no command accepts", i+1, m[1])
		}
	}
}

// A command suggested with an argument has to be suggested with the argument
// filled in.
//
// `--no-wait` exists so the caller can walk away, and the line they walk away
// with is the one they paste when they come back. Printing
// `azd ai eval job show <job-id>` reads like a command and is not one: it
// resolves, so the check above passes, and it fails the moment anyone uses it.
func TestSuggestedCommandsCarryNoPlaceholders(t *testing.T) {
	placeholder := regexp.MustCompile(`azd ai eval [^"'\n]*<[a-z-]+>`)

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
		for i, line := range strings.Split(string(body), "\n") {
			trimmed := strings.TrimSpace(line)
			// A `Use:` string and the help text around it are where a
			// placeholder belongs: cobra prints it as the signature.
			if strings.HasPrefix(trimmed, "//") ||
				strings.HasPrefix(trimmed, "Use:") ||
				strings.HasPrefix(trimmed, "Short:") ||
				strings.HasPrefix(trimmed, "Long:") {
				continue
			}
			if m := placeholder.FindString(line); m != "" {
				t.Errorf("%s:%d suggests %q; substitute the value instead",
					path, i+1, m)
			}
		}
		return nil
	})
	require.NoError(t, err)
}
