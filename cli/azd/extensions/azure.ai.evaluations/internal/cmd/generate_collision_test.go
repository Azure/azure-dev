// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// noPromptCommand is a command run the way a script runs it.
func noPromptCommand(t *testing.T) *cobra.Command {
	t.Helper()
	cmd := &cobra.Command{}
	cmd.Flags().Bool("no-prompt", true, "")
	require.NoError(t, cmd.Flags().Set("no-prompt", "true"))
	return cmd
}

// A free name is not a collision, and must not cost a prompt or a round trip.
func TestAFreeNameNeedsNoAsking(t *testing.T) {
	dir := t.TempDir()

	got, err := resolveArtifactCollision(
		nil, "Evaluator", "quality", filepath.Join(dir, "quality.json"), false)

	require.NoError(t, err)
	assert.Equal(t, "quality", got)
}

// --force is the caller having already answered. It keeps the name, which is
// what regenerating means.
func TestForceKeepsTheNameWithoutAsking(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "quality.json")
	require.NoError(t, os.WriteFile(path, []byte("{}"), 0o600))

	got, err := resolveArtifactCollision(nil, "Evaluator", "quality", path, true)

	require.NoError(t, err)
	assert.Equal(t, "quality", got)
}

// Under --no-prompt there is nobody to ask, so the refusal stands and names the
// flag that answers it. Silently regenerating would replace a checked-in file
// in CI on the strength of a name collision.
func TestNoPromptStillRefusesATakenName(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "quality.json")
	require.NoError(t, os.WriteFile(path, []byte("{}"), 0o600))

	_, err := resolveArtifactCollision(
		noPromptCommand(t), "Evaluator", "quality", path, false)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "--force")
}

// A nil command cannot reach a prompt, so it takes the same path as
// --no-prompt rather than dereferencing its way to a panic.
func TestACommandlessCallRefusesRatherThanPanics(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "quality.json")
	require.NoError(t, os.WriteFile(path, []byte("{}"), 0o600))

	assert.NotPanics(t, func() {
		_, err := resolveArtifactCollision(nil, "Evaluator", "quality", path, false)
		assert.Error(t, err)
	})
}

// The rename option proposes the first free numbered form, so the caller who
// wants to keep both artifacts is not left guessing which names are taken.
func TestTheProposedNameIsTheFirstFreeOne(t *testing.T) {
	dir := t.TempDir()
	for _, n := range []string{"quality.json", "quality-2.json", "quality-3.json"} {
		require.NoError(t, os.WriteFile(filepath.Join(dir, n), []byte("{}"), 0o600))
	}

	got := nextFreeArtifactName("quality", filepath.Join(dir, "quality.json"))

	assert.Equal(t, "quality-4", got)
}

// The extension comes from the path, so a dataset is checked against .jsonl
// rather than against whatever the evaluator uses.
func TestTheProposedNameKeepsTheArtifactsExtension(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "rows.jsonl"), []byte("{}\n"), 0o600))

	got := nextFreeArtifactName("rows", filepath.Join(dir, "rows.jsonl"))

	assert.Equal(t, "rows-2", got)
	_, err := os.Stat(filepath.Join(dir, "rows-2.jsonl"))
	assert.True(t, os.IsNotExist(err), "the proposal has to actually be free")
}

// Thirty taken forms means something other than the name is wrong, and a
// thirty-first proposal would be answering the wrong question.
func TestNoProposalWhenEveryNumberedFormIsTaken(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "quality.json"), []byte("{}"), 0o600))
	for n := 2; n <= collisionSuffixLimit; n++ {
		name := filepath.Join(dir, "quality-"+strconv.Itoa(n)+".json")
		require.NoError(t, os.WriteFile(name, []byte("{}"), 0o600))
	}

	assert.Empty(t, nextFreeArtifactName("quality", filepath.Join(dir, "quality.json")))
}
