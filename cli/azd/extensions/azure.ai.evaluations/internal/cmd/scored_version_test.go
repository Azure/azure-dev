// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"azureaieval/internal/project"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// writeCatalog writes a configuration whose dataset carries the given file and
// version, either of which may be empty.
func writeCatalog(t *testing.T, file, version string) string {
	t.Helper()

	dir := t.TempDir()
	entry := "  - name: golden"
	if file != "" {
		require.NoError(t, os.WriteFile(filepath.Join(dir, file), []byte("{\"query\":\"hi\"}\n"), 0o600))
		entry += "\n    file: ./" + file
	}
	if version != "" {
		entry += "\n    version: \"" + version + "\""
	}
	body := "datasets:\n" + entry + `
evals:
  - name: quality
    dataset: golden
    evaluators:
      - evaluator: builtin.relevance
`
	path := filepath.Join(dir, "azure.eval.yaml")
	require.NoError(t, os.WriteFile(path, []byte(body), 0o600))
	return path
}

// Reservation and reconciliation ask the same question, and the baseline they
// read it from comes over gRPC. Asking twice let one transient failure leave an
// eval unreserved and then reused -- two declarations on one id.
func TestTheRecreateDecisionIsMadeOncePerDeploy(t *testing.T) {
	group := project.Eval{Name: "nightly", Dataset: "golden"}
	digest, err := project.FingerprintGroup(group)
	require.NoError(t, err)

	env := &testEnvServer{state: map[string]string{
		project.FingerprintKey("eval", "nightly"): "a digest from some older declaration",
	}}
	r := &evalReconciler{ec: &evalContext{
		azdClient: newTestAzdClient(t, env), envName: "test",
	}}

	first, err := r.decide(context.Background(), group)
	require.NoError(t, err)
	require.True(t, first.recreate, "the fixture has to be a declaration that changed")

	// The environment now answers differently, as a failed read would.
	env.state[project.FingerprintKey("eval", "nightly")] = digest

	second, err := r.decide(context.Background(), group)
	require.NoError(t, err)
	assert.Equal(t, first, second, "the decision must not change under it")
}

// Both askers have to read the same memo, or the two can still disagree.
func TestEnsureEvalAsksTheSameMemoReservationDid(t *testing.T) {
	body, err := os.ReadFile("reconciler.go")
	require.NoError(t, err)
	source := string(body)

	assert.Contains(t, source, "r.evalDigests(ctx, group)",
		"EnsureEval has to go through the memo rather than hashing again")
	assert.Contains(t, source, "decided, err := r.decide(ctx, group)",
		"and evalDigests is what reads it")
	assert.Equal(t, 1, strings.Count(source, "project.FingerprintKey(\"eval\", group.Name))"),
		"one read of the baseline, or a transient failure flips the decision")
}
