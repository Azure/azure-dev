// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"azureaieval/internal/project"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestInitNextStepResolvesTheExactAuthoredConfiguration(t *testing.T) {
	for _, location := range []string{"", "team evals", "team evals/nightly.yaml",
		"team evals/custom.yml", "team evals/azure.eval.yaml", "team evals/eval.yaml"} {
		for _, absolute := range []bool{false, true} {
			name := location + "/relative"
			if absolute {
				name = location + "/absolute"
			}
			t.Run(name, func(t *testing.T) {
				h := newInitHarness(t, nil)
				path := filepath.FromSlash(location)
				if absolute && path != "" {
					path = filepath.Join(h.dir, path)
				}
				args := []string{"--name", "quality", "--conversation-mode", "static", "--dataset", h.seedRows,
					"--judge-model", "judge", "--no-prompt"}
				if path != "" {
					args = append(args, "--path", path)
				}
				text, err := executeConversationInit(t, args...)
				require.NoError(t, err)
				_, suffix, found := strings.Cut(text, "Next: azd ai eval create quality")
				require.True(t, found)
				line, _, _ := strings.Cut(suffix, "\n")
				nextLocation := project.DefaultEvalDir
				if line = strings.TrimSpace(line); line != "" {
					require.True(t, strings.HasPrefix(line, "--path "))
					nextLocation = strings.Trim(strings.TrimPrefix(line, "--path "), `"`)
				}
				if path == "" {
					path = project.DefaultEvalDir
					assert.Empty(t, line, "the default flow still needs no path override")
				}
				want, err := project.ResolveEvalConfigPath(path)
				require.NoError(t, err)
				got, err := project.ResolveEvalConfigPath(nextLocation)
				require.NoError(t, err)
				want, err = filepath.Abs(want)
				require.NoError(t, err)
				got, err = filepath.Abs(got)
				require.NoError(t, err)
				assert.True(t, sameFilePath(got, want), "the printed create must select the file init wrote")
				if filepath.Ext(location) != "" {
					assert.Contains(t, line, filepath.Base(path), "an explicitly selected filename must remain explicit")
				}
				cfg, err := project.OpenEvalConfig(nextLocation)
				require.NoError(t, err)
				require.NotNil(t, cfg, "the handoff must load an existing authored configuration")
				require.Len(t, cfg.Evals, 1)
				assert.Equal(t, "quality", cfg.Evals[0].Name)
			})
		}
	}
}

func TestInitNextStepPreservesUnsafeFilenameAsManualData(t *testing.T) {
	for _, name := range []string{"nightly$team.yaml", "nightly`team.yml", "%TEMP%.yaml", "nightly^team.yaml"} {
		t.Run(name, func(t *testing.T) {
			h := newInitHarness(t, nil)
			path := filepath.Join("team evals", name)
			text, err := executeConversationInit(t, "--path", path, "--name", "quality", "--conversation-mode", "static",
				"--dataset", h.seedRows, "--judge-model", "judge", "--no-prompt")
			require.NoError(t, err)
			assert.NotContains(t, text, "Next: azd ai eval create")
			assert.NotContains(t, text, "VALUE_NEEDS_QUOTING")
			assert.Contains(t, text, "no copyable command")
			assert.Contains(t, text, `Evaluation name: "quality"`)
			assert.Contains(t, text, fmt.Sprintf("--path value: %q", filepath.ToSlash(path)))
			cfg, err := project.OpenEvalConfig(path)
			require.NoError(t, err)
			require.NotNil(t, cfg)
			require.Len(t, cfg.Evals, 1)
			assert.Equal(t, "quality", cfg.Evals[0].Name)
		})
	}
}

// mustGetwd is the name the process goes by after a Chdir.
//
// Getwd resolves symlinks -- /var is /private/var on macOS -- so a directory
// taken straight from t.TempDir() and the working directory it was made into
// are two spellings of one place, and Rel between them climbs all the way up
// and back down.
func mustGetwd(t *testing.T) string {
	t.Helper()
	here, err := os.Getwd()
	require.NoError(t, err)
	return here
}

// `init --path <absolute>` wrote a project-relative `$ref` into azure.yaml and
// then printed the absolute directory back in the step beside it: one directory
// spelled two ways on one screen, and a machine-specific path in the half a
// reader is most likely to paste into a script.
//
// The printed step is relative to where it will be run from.
func TestTheNextStepNamesTheDirectoryTheWayTheRefDoes(t *testing.T) {
	here := t.TempDir()
	t.Chdir(here)
	// Re-read it: Getwd resolves symlinks, and /var is /private/var on macOS,
	// so the name the process now goes by is the one Rel has to be given.
	here = mustGetwd(t)

	s := scaffold{
		eval:    &project.Eval{Name: "an-eval"},
		evalDir: filepath.Join(here, "quality"),
	}

	steps := s.nextSteps()

	require.Len(t, steps, 1)
	assert.Contains(t, steps[0], "--path ./quality",
		"the step runs from here, so it should name the directory from here")
	assert.NotContains(t, steps[0], here,
		"nobody else's checkout has this path")
}

// A directory the reader cannot reach without climbing out of where they stand
// keeps the absolute path: `../../../elsewhere` is worse than what it replaced.
func TestAnAbsolutePathOutsideTheWorkingDirectoryStaysAbsolute(t *testing.T) {
	t.Chdir(t.TempDir())
	elsewhere := filepath.Join(t.TempDir(), "quality")

	s := scaffold{eval: &project.Eval{Name: "an-eval"}, evalDir: elsewhere}

	steps := s.nextSteps()

	require.Len(t, steps, 1)
	assert.Contains(t, steps[0], filepath.ToSlash(elsewhere))
	assert.NotContains(t, steps[0], "..", "a path that climbs out is not an improvement")
}

// The default still needs no flag at all, however it was spelled.
func TestTheDefaultDirectoryStillPrintsNoPathFlag(t *testing.T) {
	here := t.TempDir()
	t.Chdir(here)
	here = mustGetwd(t)

	s := scaffold{
		eval:    &project.Eval{Name: "an-eval"},
		evalDir: filepath.Join(here, project.DefaultEvalDir),
	}

	steps := s.nextSteps()

	require.Len(t, steps, 1)
	assert.NotContains(t, steps[0], "--path",
		"./evals is where every command already looks")
}

// A relative directory is printed as it was given: it already names the place
// from where the reader is standing.
func TestARelativeDirectoryIsPrintedAsGiven(t *testing.T) {
	s := scaffold{eval: &project.Eval{Name: "an-eval"}, evalDir: "./quality"}

	steps := s.nextSteps()

	require.Len(t, steps, 1)
	assert.Contains(t, steps[0], "--path ./quality")
}
