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

func planFor(t *testing.T, name string) generationPlan {
	t.Helper()
	return generationPlan{Name: name, BaseDir: t.TempDir(), OutputDir: "."}
}

func writeArtifact(t *testing.T, plan generationPlan, ext string) string {
	t.Helper()
	path := project.ArtifactPath(plan.BaseDir, plan.OutputDir, plan.Name, ext)
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o750))
	require.NoError(t, os.WriteFile(path, []byte("mine"), 0o600))
	return path
}

// The destination is checked before the job is submitted and written after it
// finishes, and the poll between them can run for an hour. A rubric the reader
// edited while waiting -- which is what the local file is for -- was replaced
// with no --force anywhere in the invocation.
func TestAFileThatAppearedWhileTheJobRanIsNotOverwritten(t *testing.T) {
	t.Parallel()

	plan := planFor(t, "quality")
	path := writeArtifact(t, plan, ".json")

	err := refuseArtifactThatAppeared(plan, ".json", "job_123")

	require.Error(t, err, "nothing approved replacing this file")
	assert.Contains(t, err.Error(), filepath.Base(path))
	assert.FileExists(t, path)
	body, readErr := os.ReadFile(path)
	require.NoError(t, readErr)
	assert.Equal(t, "mine", string(body), "the reader's file is untouched")
}

// The job finished and was paid for, so the refusal has to say how to collect
// it rather than leaving the reader to generate it again.
func TestTheRefusalNamesTheJobSoTheWorkIsNotLost(t *testing.T) {
	t.Parallel()

	plan := planFor(t, "quality")
	writeArtifact(t, plan, ".json")

	err := refuseArtifactThatAppeared(plan, ".json", "job_123")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "job_123", "the finished job is still collectable")
	assert.Contains(t, err.Error(), "--output-dir", "and so is writing it somewhere else")
}

// A reader who said --force, or chose to regenerate over what was there, asked
// for exactly this. The guard must not turn that into a refusal.
func TestAnApprovedReplacementStillGoesAhead(t *testing.T) {
	t.Parallel()

	plan := planFor(t, "quality")
	plan.ReplaceApproved = true
	writeArtifact(t, plan, ".json")

	assert.NoError(t, refuseArtifactThatAppeared(plan, ".json", "job_123"),
		"--force is the approval this is checking for")
}

// The ordinary path: nothing turned up, so nothing is in the way.
func TestAFreeDestinationIsNotRefused(t *testing.T) {
	t.Parallel()

	plan := planFor(t, "quality")

	assert.NoError(t, refuseArtifactThatAppeared(plan, ".json", "job_123"))
}

// Datasets take the same window and the same guard, on their own extension.
func TestTheDatasetDestinationIsGuardedToo(t *testing.T) {
	t.Parallel()

	plan := planFor(t, "golden")
	writeArtifact(t, plan, ".jsonl")

	require.Error(t, refuseArtifactThatAppeared(plan, ".jsonl", "job_456"))
	assert.NoError(t, refuseArtifactThatAppeared(plan, ".json", "job_456"),
		"a different artifact of the same name is not this one")
}

// `--no-wait` returns before there is a job to collect, and a reattached
// collection has no job id to quote. The refusal still has to stand.
func TestARefusalWithoutAJobIDStillRefuses(t *testing.T) {
	t.Parallel()

	plan := planFor(t, "quality")
	writeArtifact(t, plan, ".json")

	err := refuseArtifactThatAppeared(plan, ".json", "")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "--force", "still says what answers it")
}
