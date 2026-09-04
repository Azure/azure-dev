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

// The label a run carries has to follow the same branch its rows did.
//
// A declaration with both `file:` and `version:` reads the file from disk, so
// the pin says nothing about what was scored: the recorded version is the one
// this file's content published, and checkDatasetRegistered has already
// confirmed the rows match it.
func TestARunOverALocalFileIsLabelledWithWhatTheFilePublished(t *testing.T) {
	env := &testEnvServer{state: map[string]string{
		versionKey("dataset", "golden"):             "2",
		project.FingerprintKey("dataset", "golden"): "a digest of the file",
	}}
	ec := &evalContext{azdClient: newTestAzdClient(t, env), envName: "test"}
	group := &project.Eval{Name: "quality", Dataset: "golden"}

	labelled := ec.scoredDatasetVersion(
		context.Background(), group, writeCatalog(t, "golden.jsonl", "1"))

	assert.Equal(t, "2", labelled,
		"the rows came off disk, so the pin is not what was scored")
}

// And with no fingerprint there is nothing tying the file to that version. A
// dataset that was registered and has since gained a `file:` has a recorded
// version and no fingerprint, and the rows are whatever the file now holds.
func TestALocalFileWithNoFingerprintIsLabelledWithNothing(t *testing.T) {
	env := &testEnvServer{state: map[string]string{
		versionKey("dataset", "golden"): "3",
	}}
	ec := &evalContext{azdClient: newTestAzdClient(t, env), envName: "test"}
	group := &project.Eval{Name: "quality", Dataset: "golden"}

	labelled := ec.scoredDatasetVersion(
		context.Background(), group, writeCatalog(t, "golden.jsonl", ""))

	assert.Empty(t, labelled,
		"no version is better than one the rows are not from")
}

// A registered dataset is fetched at the pin, so the pin is the honest label.
func TestARunOverARegisteredDatasetIsLabelledWithThePin(t *testing.T) {
	env := &testEnvServer{state: map[string]string{
		versionKey("dataset", "golden"): "2",
	}}
	ec := &evalContext{azdClient: newTestAzdClient(t, env), envName: "test"}
	group := &project.Eval{Name: "quality", Dataset: "golden"}

	labelled := ec.scoredDatasetVersion(
		context.Background(), group, writeCatalog(t, "", "1"))

	assert.Equal(t, "1", labelled, "the pin is the version the rows were read at")
}

// With no pin either way, the recorded version answers.
func TestWithoutAPinTheRecordedVersionIsTheLabel(t *testing.T) {
	env := &testEnvServer{state: map[string]string{
		versionKey("dataset", "golden"):             "2",
		project.FingerprintKey("dataset", "golden"): "a digest of the file",
	}}
	ec := &evalContext{azdClient: newTestAzdClient(t, env), envName: "test"}
	group := &project.Eval{Name: "quality", Dataset: "golden"}

	assert.Equal(t, "2", ec.scoredDatasetVersion(
		context.Background(), group, writeCatalog(t, "", "")))
	assert.Equal(t, "2", ec.scoredDatasetVersion(
		context.Background(), group, writeCatalog(t, "golden.jsonl", "")))
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
