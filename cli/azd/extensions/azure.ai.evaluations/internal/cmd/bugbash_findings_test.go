// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// `--evaluator` becomes ./evaluators/<ref>.json, so a reference that can climb
// out of that directory picks a file the reader never meant to publish.
// `--evaluator ../../id_rsa` scaffolded a source outside the project, and
// deploy reads and uploads whatever a declared source resolves to.
func TestEvaluatorRefCannotClimbOutOfTheEvaluatorsDirectory(t *testing.T) {
	escapes := []string{
		"../secrets",
		"../../id_rsa",
		"a/b",
		`a\b`,
		"C:/Windows/System32/config/SAM",
		"..",
		".",
	}
	for _, ref := range escapes {
		t.Run(ref, func(t *testing.T) {
			err := validateEvaluatorRefs([]string{ref})

			require.Error(t, err, "a reference naming a path was accepted")
			assert.Contains(t, err.Error(), "path",
				"the refusal has to say what is wrong with it")
		})
	}
}

// The names people actually pass still work.
func TestOrdinaryEvaluatorRefsAreStillAccepted(t *testing.T) {
	for _, ref := range []string{"quality", "builtin.relevance", "support-agent-quality", "my_eval.v2"} {
		assert.NoError(t, validateEvaluatorRefs([]string{ref}), "%q is a name, not a path", ref)
	}
}

// An absolute --path is documented as supported. Prefixing it with "./" made
// the service `$ref` read `.//tmp/evals/azure.eval.yaml`, so `init` wrote the
// configuration where it was asked and `azd up` resolved a different file under
// the project -- the scaffold the reader was looking at was never deployed.
func TestRefToLeavesAnAbsolutePathAlone(t *testing.T) {
	abs := filepath.Join(t.TempDir(), "evals", "azure.eval.yaml")
	require.True(t, filepath.IsAbs(abs), "the fixture has to be absolute to test this")

	got := refTo(abs)

	assert.False(t, strings.HasPrefix(got, "./"),
		"an absolute path is already a path; %q is not one", got)
	assert.Equal(t, filepath.ToSlash(abs), got)
}

// A relative one still gets the prefix, so the directive reads as a path rather
// than as a registry name.
func TestRefToMarksARelativePath(t *testing.T) {
	assert.Equal(t, "./evals/azure.eval.yaml",
		refTo(filepath.Join("evals", "azure.eval.yaml")))
}

// `generate` and the named verbs disagree about what a name may contain, and
// both sides are deliberate: nameIsAPathComponent checks only what the
// filesystem objects to, so it does not refuse names the service accepts, while
// validAssetName models the service's own character set so a typo is reported
// here instead of coming back as a 400 wrapped in JSON.
//
// The gap between them is reachable: `generate --dataset-name "my data"`
// publishes, and `dataset versions list "my data"` then refuses to name it.
// Pinned rather than fixed, because closing it means overruling one of those
// two decisions and that is the owner's call, not a late edit's.
func TestGenerateAndLookupDisagreeOnNames(t *testing.T) {
	const spaced = "my data"

	generated, err := generatedName(spaced, "support-agent", "dataset")
	require.NoError(t, err, "generate accepts a spaced name")
	require.Equal(t, spaced, generated)

	assert.False(t, validAssetName(spaced),
		"and the named verbs refuse it, so what generate just published cannot be listed")
}
