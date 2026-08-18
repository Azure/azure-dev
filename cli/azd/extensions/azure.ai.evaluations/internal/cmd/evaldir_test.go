// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"errors"
	"strings"
	"testing"

	"azureaieval/internal/project"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Where the configuration lives is settled by one cascade -- --path, then the
// path `init` recorded in the azd environment, then ./evals -- and these tests
// pin it.
//
// `init --path ./quality` wrote a configuration that `run` then looked for
// under ./evals and reported as missing, while azure.yaml's $ref pointed at it
// correctly the whole time. The path init used is remembered so the flag does
// not have to be repeated on every later command.
//
// That fix reached `run` and stopped there. In a project scaffolded outside
// ./evals, `create` went on reporting the configuration missing and `generate`
// went on submitting a billed job and writing a *second* configuration under
// ./evals that nothing else read. So these tests are written over every
// command, not over the one that was wrong at the time.

func TestEvalDirCascade(t *testing.T) {
	// No azd environment: there is nothing to read, so only flag and default apply.
	ec := &evalContext{}

	dir, err := ec.evalDir(context.Background(), "")
	require.NoError(t, err)
	assert.Equal(t, project.DefaultEvalDir, dir, "nothing given anywhere is ./evals")

	dir, err = ec.evalDir(context.Background(), "quality")
	require.NoError(t, err)
	assert.Equal(t, "quality", dir, "--path wins")
}

func TestEvalDirCascadeAnswersInOrder(t *testing.T) {
	cases := []struct {
		name     string
		flag     string
		recorded string
		want     string
	}{
		{
			name:     "the flag wins",
			flag:     "./given",
			recorded: "./recorded",
			want:     "./given",
		},
		{
			name:     "the flag wins even over nothing recorded",
			flag:     "./given",
			recorded: "",
			want:     "./given",
		},
		{
			name:     "what init recorded is used when no flag was given",
			flag:     "",
			recorded: "./quality",
			want:     "./quality",
		},
		{
			name:     "the default is the last resort",
			flag:     "",
			recorded: "",
			want:     project.DefaultEvalDir,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := evalDirCascade(tc.flag, func() (string, error) {
				return tc.recorded, nil
			})
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

// A read that failed is not a project that recorded nothing. Defaulting on it
// is how `generate` would write a second configuration under ./evals for a
// reason nobody could reproduce, so the failure has to come back out.
func TestEvalDirCascadeDoesNotDefaultOnAFailedRead(t *testing.T) {
	boom := errors.New("the environment could not be read")

	got, err := evalDirCascade("", func() (string, error) { return "", boom })

	require.ErrorIs(t, err, boom)
	assert.Empty(t, got, "a failed read must not answer with the default")
}

// A --path that was given is the answer on its own, so a broken azd cannot
// stop a caller who already said where to look.
func TestEvalDirCascadeIgnoresAFailedReadWhenPathWasGiven(t *testing.T) {
	got, err := evalDirCascade("./given", func() (string, error) {
		return "", errors.New("the environment could not be read")
	})

	require.NoError(t, err)
	assert.Equal(t, "./given", got)
}

// Each read is a round trip, and a --path that was given makes it unnecessary.
func TestEvalDirCascadeAsksForTheRecordedPathOnce(t *testing.T) {
	var asked int
	got, err := evalDirCascade("", func() (string, error) {
		asked++
		return "", nil
	})

	require.NoError(t, err)
	assert.Equal(t, project.DefaultEvalDir, got)
	assert.Equal(t, 1, asked, "the recorded path should be read exactly once")

	asked = 0
	_, err = evalDirCascade("./given", func() (string, error) {
		asked++
		return "", nil
	})
	require.NoError(t, err)
	assert.Equal(t, 0, asked, "a --path that was given should not cost a round trip")
}

// --path defaults to empty, not to ./evals, so "not given" stays
// distinguishable from "given the default". A non-empty default shadows the
// path init recorded, because level 1 only yields on an empty value -- so the
// command has opted out of the cascade without saying so.
//
// This was asserted for `run start` alone, which is exactly how `create`,
// `generate` and `init` came to be filling the default in. It is written over
// the whole tree now.
func TestPathFlagsLeaveRoomForTheRecordedPath(t *testing.T) {
	var checked int
	walk(t, NewRootCommand(), nil, func(name string, cmd *cobra.Command) {
		f := cmd.Flags().Lookup("path")
		if f == nil {
			return
		}
		checked++
		assert.Empty(t, f.DefValue,
			"`azd ai eval %s --path` defaults to %q, so the path `init` recorded can "+
				"never be reached: level 1 of the cascade only yields on an empty value",
			name, f.DefValue)
	})

	// If --path is ever renamed, the loop above passes by visiting nothing.
	assert.GreaterOrEqual(t, checked, 3,
		"expected --path on at least init, generate and eval create; found %d", checked)
}

// Every command that reads the configuration has to be able to say where it is,
// or a project scaffolded with --path is unreachable from that command.
//
// `create` was missing from this list, and was one of the two commands that
// could not find a configuration outside ./evals.
func TestCommandsReadingTheConfigTakePath(t *testing.T) {
	for _, path := range []string{"run start", "init", "generate", "create"} {
		cmd := find(t, path)
		assert.NotNilf(t, cmd.Flags().Lookup("path"),
			"%s reads the configuration, so it must accept --path", path)
	}
}

// Guards against the tree walk above passing because the flag was renamed.
func TestPathFlagIsStillCalledPath(t *testing.T) {
	var names []string
	walk(t, NewRootCommand(), nil, func(name string, cmd *cobra.Command) {
		cmd.LocalFlags().VisitAll(func(f *pflag.Flag) {
			if f.Name == "path" {
				names = append(names, name)
			}
		})
	})

	for _, want := range []string{"init", "generate", "create", "run start"} {
		assert.Contains(t, names, want)
	}
}

// The recorded key is what `init` writes and what the other commands read; a
// rename on one side alone silently stops the hand-off working.
func TestEvalPathEnvKey(t *testing.T) {
	assert.Equal(t, "EVAL_CONFIG_PATH", envKeyEvalPath)
}

// `init` prints the commands to run next, and the claim those lines make is
// that they run as printed. A scaffold written somewhere other than ./evals is
// only reachable by a command that names it, because EVAL_CONFIG_PATH is
// recorded best effort and `init` succeeds without an azd environment to record
// it in.
func TestNextStepsRunAsPrinted(t *testing.T) {
	cases := []struct {
		name      string
		evalDir   string
		deployCmd string
		wantPath  bool
	}{
		{
			name:      "a scaffold outside ./evals names itself",
			evalDir:   "./quality",
			deployCmd: "azd ai eval create",
			wantPath:  true,
		},
		{
			name:      "the default directory needs no flag",
			evalDir:   project.DefaultEvalDir,
			deployCmd: "azd ai eval create",
			wantPath:  false,
		},
		{
			name:      "an unrecorded directory needs no flag",
			evalDir:   "",
			deployCmd: "azd ai eval create",
			wantPath:  false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := scaffold{eval: &project.Eval{Name: "an-eval"}, evalDir: tc.evalDir}
			for _, step := range s.nextSteps(tc.deployCmd) {
				assert.Equal(t, tc.wantPath, strings.Contains(step, "--path "),
					"step %q", step)
			}
		})
	}
}

// `azd up` provisions and then deploys, reading azure.yaml -- which already
// $refs the configuration wherever it was written. It takes none of this
// extension's flags, so handing it --path prints a step that fails.
func TestNextStepsNeverFlagAzdUp(t *testing.T) {
	s := scaffold{eval: &project.Eval{Name: "an-eval"}, evalDir: "./quality"}

	steps := s.nextSteps(azdUpCommand)
	assert.Contains(t, steps, azdUpCommand,
		"`azd up` should be suggested exactly as it is run")
	for _, step := range steps {
		if strings.HasPrefix(step, azdUpCommand) {
			assert.NotContains(t, step, "--path", "step %q", step)
		}
	}
}

// A generated next step already carried --target and --generation-model for the
// same reason. --path joins them.
func TestGenerateStepNamesTheScaffoldedDirectory(t *testing.T) {
	s := scaffold{
		eval:            &project.Eval{Name: "an-eval"},
		evalDir:         "./quality",
		target:          "support-agent",
		judgeModel:      "gpt-4.1-nano",
		rubricName:      "support-agent-quality",
		datasetName:     "support-agent-dataset",
		generateDataset: true,
		generateRubric:  true,
	}

	steps := s.nextSteps("azd ai eval create")
	if assert.Len(t, steps, 1) {
		for _, want := range []string{
			"--target support-agent",
			"--generation-model gpt-4.1-nano",
			"--path ./quality",
		} {
			assert.Contains(t, steps[0], want)
		}
	}
}

// A directory with a space in it printed `--path ./team evals`, which resolves
// ./team and reports the configuration missing -- the printed step failing in
// the one case it was added for. Found by running it, not by reading it.
func TestNextStepQuotesADirectoryThatNeedsIt(t *testing.T) {
	cases := []struct {
		name    string
		evalDir string
		want    string
	}{
		{
			name:    "a space",
			evalDir: "./team evals",
			want:    `--path "./team evals"`,
		},
		{
			name:    "a windows path with a space",
			evalDir: `C:\Users\Me\My Evals`,
			want:    `--path "C:\Users\Me\My Evals"`,
		},
		{
			name:    "a plain relative path is left alone",
			evalDir: "./quality",
			want:    "--path ./quality",
		},
		{
			name:    "a plain windows path is left alone",
			evalDir: `C:\Users\Me\quality`,
			want:    `--path C:\Users\Me\quality`,
		},
		{
			name:    "a character the shell would expand",
			evalDir: "./eval$dir",
			want:    `--path "./eval$dir"`,
		},
		{
			name:    "a character that would end the command",
			evalDir: "./a;rm -rf b",
			want:    `--path "./a;rm -rf b"`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := scaffold{eval: &project.Eval{Name: "an-eval"}, evalDir: tc.evalDir}
			steps := s.nextSteps("azd ai eval create")
			require.NotEmpty(t, steps)
			for _, step := range steps {
				assert.Contains(t, step, tc.want, "step %q", step)
			}
		})
	}
}

// Backslashes must survive: doubling them is right for bash and wrong for the
// two shells most likely to be reading a path that looks like this.
func TestQuoteForShellLeavesBackslashesAlone(t *testing.T) {
	assert.Equal(t, `"C:\Users\Me\My Evals"`, quoteForShell(`C:\Users\Me\My Evals`))
	assert.Equal(t, `C:\Users\Me\Evals`, quoteForShell(`C:\Users\Me\Evals`))
}
