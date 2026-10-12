// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// answeringCatalogue makes the project answer with these built-ins, and
// records whether it was asked at all.
func answeringCatalogue(offered ...string) (func(context.Context) []string, *int) {
	asked := 0
	return func(context.Context) []string {
		asked++
		return offered
	}, &asked
}

// initIn builds the action `azd ai eval init` runs, scaffolding into dir.
func initIn(
	t *testing.T, dir string, catalogue func(context.Context) []string, evaluators ...string,
) (*initAction, *bytes.Buffer) {
	t.Helper()

	out := &bytes.Buffer{}
	cmd := &cobra.Command{Use: "init"}
	cmd.SetOut(out)
	cmd.SetErr(out)
	cmd.SetContext(t.Context())
	// The flags Run() reads off the command rather than the struct.
	cmd.Flags().Int("max-traces", 0, "")
	cmd.Flags().Bool("no-prompt", true, "")

	return &initAction{
		cmd:           cmd,
		knownBuiltins: catalogue,
		flags: &initFlags{
			evalName:   "quality",
			target:     "support-agent",
			source:     initSourceDataset,
			dataset:    "support-golden",
			evaluators: evaluators,
			path:       dir,
		},
	}, out
}

// scaffoldedAnything reports whether init left anything behind in dir.
func scaffoldedAnything(t *testing.T, dir string) bool {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return false
	}
	require.NoError(t, err)
	return len(entries) > 0
}

// ADO 5631310: `init --evaluator builtin.does_not_exist` wrote an eval that
// create and run could not resolve.
//
// This drives the orchestration in `initAction.Run`, not the pieces. The gate,
// the lookup and the refusal each have their own tests, and all three stay
// green if the block that wires them together is deleted -- which is exactly
// how the defect would come back.
func TestInitRefusesAnUnknownBuiltinBeforeWritingAnything(t *testing.T) {
	t.Parallel()

	catalogue, asked := answeringCatalogue("builtin.coherence", "builtin.groundedness")

	dir := filepath.Join(t.TempDir(), "evals")
	action, _ := initIn(t, dir, catalogue, "builtin.does_not_exist")

	err := action.Run()

	require.Error(t, err, "init accepted a builtin the project does not offer")
	assert.Contains(t, err.Error(), "builtin.does_not_exist")
	assert.Contains(t, err.Error(), "builtin.coherence",
		"the error lists what the project does offer")

	assert.Equal(t, 1, *asked, "the catalogue is asked once")
	assert.False(t, scaffoldedAnything(t, dir),
		"a refused init must not leave a half-scaffolded tree behind")
}

// The gate is not a silent skip: a reference the catalogue does offer still
// reaches the check and still passes it, so the refusal above is a verdict
// rather than init failing on every builtin.
func TestInitAcceptsABuiltinTheCatalogueOffers(t *testing.T) {
	t.Parallel()

	catalogue, asked := answeringCatalogue("builtin.coherence", "builtin.groundedness")

	dir := filepath.Join(t.TempDir(), "evals")
	action, _ := initIn(t, dir, catalogue, "builtin.coherence")

	// The scaffold itself needs an azd project, which this test has no way to
	// supply, so the assertion is that it got past the catalogue -- the
	// reference was not what stopped it.
	err := action.Run()

	assert.Equal(t, 1, *asked, "the catalogue is asked once")
	if err != nil {
		assert.NotContains(t, err.Error(), "builtin.coherence",
			"a reference the project offers must not be what init refused")
	}
}

// The lookup authenticates and can wait out its own timeout, so `init` must not
// make it when there is nothing for the catalogue to answer about. This is the
// orchestration half of the gate; hasBuiltinRef's own tests cover the predicate.
func TestInitDoesNotAskTheCatalogueWithoutABuiltinReference(t *testing.T) {
	t.Parallel()

	for _, name := range []string{"no --evaluator at all", "only custom references"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			catalogue, asked := answeringCatalogue("builtin.coherence")

			var refs []string
			if name == "only custom references" {
				refs = []string{"support-quality", "tone-check"}
			}
			action, _ := initIn(t, filepath.Join(t.TempDir(), "evals"), catalogue, refs...)
			_ = action.Run()

			assert.Zero(t, *asked,
				"init opened a connection that could not have validated anything")
		})
	}
}
