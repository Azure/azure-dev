// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"strings"
	"testing"

	"azureaieval/internal/project"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/stretchr/testify/assert"
)

// Where the configuration lives is settled by one cascade -- --path, then the
// path `init` recorded in the azd environment, then ./evals -- and these tests
// pin the two halves of it that have been got wrong.
//
// The bug they were written for: `init --path ./quality` recorded
// EVAL_CONFIG_PATH and scaffolded there, and then `azd ai eval create` reported
// "no eval configuration at evals/azure.eval.yaml" while `azd ai eval generate`
// silently billed a generation job and wrote a *second* configuration under
// ./evals that nothing else read. `run` had the cascade; the commands that
// write did not.

// A --path that defaults to "evals" can never reach level 2, because level 1
// only yields when the flag is empty. So the default is the invariant: a
// command that fills it in has opted out of the cascade without saying so, and
// that is exactly how create, generate and init came to skip it.
func TestPathFlagsLeaveRoomForTheRecordedPath(t *testing.T) {
	root := NewRootCommand()

	var checked int
	walk(t, root, nil, func(name string, cmd *cobra.Command) {
		f := cmd.Flags().Lookup("path")
		if f == nil {
			return
		}
		checked++
		assert.Empty(t, f.DefValue,
			"`azd ai %s --path` defaults to %q, so the path `init` recorded can "+
				"never be reached: level 1 of the cascade only yields on an empty value",
			name, f.DefValue)
	})

	// If --path is ever renamed, the loop above passes by visiting nothing.
	assert.GreaterOrEqual(t, checked, 3,
		"expected --path on at least init, generate and eval create; found %d", checked)
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
			got := evalDirCascade(tc.flag, func() string { return tc.recorded })
			assert.Equal(t, tc.want, got)
		})
	}
}

// The azd environment is not always readable -- `init` works without one -- so
// level 2 can come back empty for a reason that is not "nothing was recorded".
// Falling through to the default is right; asking twice is not, because each
// call is a round trip.
func TestEvalDirCascadeAsksForTheRecordedPathOnce(t *testing.T) {
	var asked int
	got := evalDirCascade("", func() string {
		asked++
		return ""
	})

	assert.Equal(t, project.DefaultEvalDir, got)
	assert.Equal(t, 1, asked, "the recorded path should be read exactly once")

	asked = 0
	evalDirCascade("./given", func() string {
		asked++
		return ""
	})
	assert.Equal(t, 0, asked, "a --path that was given should not cost a round trip")
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
			s := scaffold{
				eval:    &project.Eval{Name: "an-eval"},
				evalDir: tc.evalDir,
			}
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
	s := scaffold{
		eval:    &project.Eval{Name: "an-eval"},
		evalDir: "./quality",
	}

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

// Guards against the loop above passing because pflag stopped reporting
// defaults the way this test reads them.
func TestPathFlagIsStillCalledPath(t *testing.T) {
	var names []string
	walk(t, NewRootCommand(), nil, func(name string, cmd *cobra.Command) {
		cmd.LocalFlags().VisitAll(func(f *pflag.Flag) {
			if f.Name == "path" {
				names = append(names, name)
			}
		})
	})

	assert.Contains(t, names, "init")
	assert.Contains(t, names, "generate")
	assert.Contains(t, names, "create")
	assert.Contains(t, names, "run start")
}
