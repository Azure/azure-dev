// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"azureaieval/internal/project"

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

// ADO 5653254: implicit defaults bypassed the catalogue gate, so adding a
// default that had not reached a project yet produced a scaffold that failed
// only at create time.
func TestInitRefusesAnUnavailableCompositeDefaultBeforeWritingAnything(t *testing.T) {
	t.Parallel()

	catalogue, asked := answeringCatalogue("builtin.output_quality")
	dir := filepath.Join(t.TempDir(), "evals")
	action, _ := initIn(t, dir, catalogue)

	err := action.Run()

	require.Error(t, err)
	assert.Contains(t, err.Error(), "builtin.tool_use_quality")
	assert.Equal(t, 1, *asked, "the default set is checked against the catalogue once")
	assert.False(t, scaffoldedAnything(t, dir))
}

// An explicit custom-only set replaces the built-in defaults, so the built-in
// catalogue has nothing to validate and must not be opened.
func TestInitDoesNotAskTheCatalogueForOnlyCustomReferences(t *testing.T) {
	t.Parallel()

	catalogue, asked := answeringCatalogue("builtin.output_quality")
	action, _ := initIn(t, filepath.Join(t.TempDir(), "evals"),
		catalogue, "support-quality", "tone-check")

	_ = action.Run()

	assert.Zero(t, *asked,
		"init opened a connection that could not have validated anything")
}

func TestInitCatalogueCriteriaReachTheAuthoredConfig(t *testing.T) {
	for _, tc := range []struct {
		name  string
		known []string
		ref   string
		fail  bool
	}{
		{"authoritative unknown", []string{"builtin.coherence"}, "builtin.does_not_exist", true},
		{"offered built-in", []string{"builtin.coherence"}, "builtin.coherence", false},
		{"outside picker", []string{"builtin.relevance"}, "builtin.relevance", false},
		{"bare catalog spelling", []string{"relevance"}, "builtin.relevance", false},
		{"unavailable catalog", nil, "builtin.unverified", false},
		{"empty catalog", []string{}, "builtin.unverified", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newInitHarness(t, nil)
			before := initFileSnapshot(t, h.dir)
			catalogue, asked := answeringCatalogue(tc.known...)
			path := filepath.Join(h.dir, "team evals", "quality.yml")
			action, out := initIn(t, path, catalogue, tc.ref)
			action.flags.judgeModel = "judge"
			action.cmd.Flags().String("output", "json", "")
			err := action.Run()
			assert.Equal(t, 1, *asked)
			if tc.fail {
				require.ErrorContains(t, err, tc.ref)
				assert.Empty(t, out.String())
				assert.Zero(t, h.project.wiringAttempts())
				assert.Equal(t, before, initFileSnapshot(t, h.dir))
				return
			}
			require.NoError(t, err)
			assert.True(t, json.Valid(out.Bytes()))
			cfg, err := project.OpenEvalConfig(path)
			require.NoError(t, err)
			require.Len(t, cfg.Evals, 1)
			require.Len(t, cfg.Evals[0].Evaluators, 1)
			assert.Equal(t, tc.ref, cfg.Evals[0].Evaluators[0].Evaluator)
			assert.Equal(t, 1, h.project.wiringAttempts())
		})
	}
}
