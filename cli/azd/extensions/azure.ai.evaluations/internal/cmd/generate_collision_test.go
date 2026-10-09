// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"azureaieval/internal/messages"

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

	got, replace, err := resolveArtifactCollision(
		nil, "Evaluator", "quality", filepath.Join(dir, "quality.json"), false)

	require.NoError(t, err)
	assert.Equal(t, "quality", got)
	assert.False(t, replace, "nothing was there, so nothing was approved for replacing")
}

// --force is the caller having already answered. It keeps the name, which is
// what regenerating means.
func TestForceKeepsTheNameWithoutAsking(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "quality.json")
	require.NoError(t, os.WriteFile(path, []byte("{}"), 0o600))

	got, replace, err := resolveArtifactCollision(nil, "Evaluator", "quality", path, true)

	require.NoError(t, err)
	assert.Equal(t, "quality", got)
	assert.True(t, replace, "--force is the approval to write over it")
}

// Under --no-prompt there is nobody to ask, so the refusal stands and names the
// flag that answers it. Silently regenerating would replace a checked-in file
// in CI on the strength of a name collision.
func TestNoPromptStillRefusesATakenName(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "quality.json")
	require.NoError(t, os.WriteFile(path, []byte("{}"), 0o600))

	_, _, err := resolveArtifactCollision(
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
		_, _, err := resolveArtifactCollision(nil, "Evaluator", "quality", path, false)
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

	got := nextFreeArtifactName("quality", filepath.Join(dir, "quality.json"), 0)

	assert.Equal(t, "quality-4", got)
}

// The extension comes from the path, so a dataset is checked against .jsonl
// rather than against whatever the evaluator uses.
func TestTheProposedNameKeepsTheArtifactsExtension(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "rows.jsonl"), []byte("{}\n"), 0o600))

	got := nextFreeArtifactName("rows", filepath.Join(dir, "rows.jsonl"), 0)

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

	assert.Empty(t, nextFreeArtifactName("quality", filepath.Join(dir, "quality.json"), 0))
}

func TestUnicodeCollisionProposalPreservesRuneLimit(t *testing.T) {
	for _, name := range []string{"caf\u00e9", "\u6570\u636e\U0001f331"} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, name+".jsonl")
			for n := 2; n <= 9; n++ {
				require.NoError(t, os.WriteFile(filepath.Join(dir, name+"-"+strconv.Itoa(n)+".jsonl"),
					[]byte("{}\n"), 0o600))
			}
			limit := len([]rune(name)) + 3
			assert.Equal(t, name+"-10", nextFreeArtifactName(name, path, limit))
			assert.Empty(t, nextFreeArtifactName(name, path, limit-1),
				"two-digit suffix must not exceed the rune limit")
			assert.Equal(t, name+"-10", nextFreeArtifactName(name, path, 0),
				"unbounded evaluator proposals preserve Unicode too")
		})
	}
}

func TestDatasetCollisionProposalRespectsGenerationNameLimit(t *testing.T) {
	dir := t.TempDir()
	name := strings.Repeat("a", 48)
	path := filepath.Join(dir, name+".jsonl")
	proposed := nextFreeArtifactName(name, path, generatedDatasetNameMaxLength)
	assert.Len(t, proposed, 50)
	assert.True(t, strings.HasSuffix(proposed, "-2"))
	require.NoError(t, os.WriteFile(filepath.Join(dir, proposed+".jsonl"), []byte("{}"), 0o600))
	next := nextFreeArtifactName(name, path, generatedDatasetNameMaxLength)
	assert.Len(t, next, 50)
	assert.True(t, strings.HasSuffix(next, "-3"))
	assert.Equal(t, name+"-2", nextFreeArtifactName(name, filepath.Join(dir, name+".json"), 0),
		"evaluator proposals keep their existing behavior")
	assert.Empty(t, nextFreeArtifactName(strings.Repeat("a", 50), path, generatedDatasetNameMaxLength))
}

func TestDatasetCollisionPickerOffersBoundedRename(t *testing.T) {
	t.Setenv("AZD_NO_PROMPT", "false")
	prompts := &conversationPromptServer{decision: collisionRename}
	h := newInitHarness(t, nil, prompts)
	name := strings.Repeat("a", 48)
	path := filepath.Join(h.dir, name+".jsonl")
	require.NoError(t, os.WriteFile(path, []byte("{}"), 0o600))
	before := initFileSnapshot(t, h.dir)
	cmd := generateCmd(t, false)
	cmd.SetContext(t.Context())
	chosen, replace, err := resolveArtifactCollision(cmd, "Dataset", name, path, false)
	require.NoError(t, err)
	assert.Len(t, chosen, 50)
	assert.True(t, strings.HasSuffix(chosen, "-2"))
	assert.False(t, replace)
	assert.Equal(t, before, initFileSnapshot(t, h.dir))
}

func TestDatasetCollisionPickerDoesNotTruncateExplicitNames(t *testing.T) {
	t.Setenv("AZD_NO_PROMPT", "false")
	prompts := &instructionPromptServer{choice: new(int32(0))}
	h := newInitHarness(t, nil, prompts)
	name := strings.Repeat("a", 30) + "-conversation-tests"
	require.Len(t, name, 49)
	path := filepath.Join(h.dir, name+".jsonl")
	require.NoError(t, os.WriteFile(path, []byte("{}"), 0o600))
	before := initFileSnapshot(t, h.dir)
	cmd := generateCmd(t, false)
	cmd.SetContext(t.Context())
	got, replace, err := resolveArtifactCollision(cmd, "Dataset", name, path, false)
	require.NoError(t, err)
	assert.Equal(t, name, got)
	assert.True(t, replace)
	prompts.mu.Lock()
	defer prompts.mu.Unlock()
	assert.Equal(t, []string{messages.RegenerateArtifactChoice(), messages.CancelGenerationChoice()}, prompts.choices)
	assert.Equal(t, before, initFileSnapshot(t, h.dir))
	assert.Empty(t, nextFreeArtifactName(name, path, generatedDatasetNameMaxLength))
}
