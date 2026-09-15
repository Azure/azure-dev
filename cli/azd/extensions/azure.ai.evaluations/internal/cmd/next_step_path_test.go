// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"azureaieval/internal/project"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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
	assert.Contains(t, steps[0], elsewhere)
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
