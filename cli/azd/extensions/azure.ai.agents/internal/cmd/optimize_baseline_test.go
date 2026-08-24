// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"azureaiagent/internal/pkg/agents/opt_eval"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// writeAgentConfigDir creates a minimal agent config version directory with the
// given instruction text and optional skill files under configsDir/<name>.
func writeAgentConfigDir(t *testing.T, configsDir, name, instruction string, skills map[string]string) {
	t.Helper()
	dir := filepath.Join(configsDir, name)
	require.NoError(t, os.MkdirAll(dir, 0750))
	require.NoError(t, os.WriteFile(
		filepath.Join(dir, opt_eval.MetadataFile),
		[]byte("instruction_file: "+opt_eval.InstructionFile+"\n"), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, opt_eval.InstructionFile), []byte(instruction), 0600))
	for rel, content := range skills {
		p := filepath.Join(dir, rel)
		require.NoError(t, os.MkdirAll(filepath.Dir(p), 0750))
		require.NoError(t, os.WriteFile(p, []byte(content), 0600))
	}
}

func TestAdvanceBaselineToCandidate_ReplacesBaseline(t *testing.T) {
	t.Parallel()

	serviceDir := t.TempDir()
	configsDir := filepath.Join(serviceDir, opt_eval.AgentConfigsDir)

	// Baseline holds the original config plus a stale skill file that the
	// candidate no longer has; it must not survive the swap.
	writeAgentConfigDir(t, configsDir, opt_eval.BaselineDir, "original instructions",
		map[string]string{filepath.Join(opt_eval.SkillsDir, "old.md"): "old skill"})
	writeAgentConfigDir(t, configsDir, "candidate_abc", "optimized instructions",
		map[string]string{filepath.Join(opt_eval.SkillsDir, "new.md"): "new skill"})

	require.NoError(t, advanceBaselineToCandidate(serviceDir, "candidate_abc", "job-1"))

	baselineDir := filepath.Join(configsDir, opt_eval.BaselineDir)

	// Baseline now carries the candidate's instructions.
	got, err := os.ReadFile(filepath.Join(baselineDir, opt_eval.InstructionFile))
	require.NoError(t, err)
	assert.Equal(t, "optimized instructions", string(got))

	// The candidate's skill replaced the stale baseline skill (clean replace).
	_, err = os.Stat(filepath.Join(baselineDir, opt_eval.SkillsDir, "new.md"))
	assert.NoError(t, err)
	_, err = os.Stat(filepath.Join(baselineDir, opt_eval.SkillsDir, "old.md"))
	assert.True(t, os.IsNotExist(err), "stale baseline skill should be removed")

	// The previous baseline is archived (not deleted) as baseline_<job-id>.
	archived, err := os.ReadFile(
		filepath.Join(configsDir, opt_eval.BaselineDir+"_job-1", opt_eval.InstructionFile))
	require.NoError(t, err)
	assert.Equal(t, "original instructions", string(archived))
	_, err = os.Stat(filepath.Join(configsDir, opt_eval.BaselineDir+"_job-1", opt_eval.SkillsDir, "old.md"))
	assert.NoError(t, err, "archived baseline should retain its original files")

	// The candidate directory is copied, not moved — it must still exist so the
	// deploy pipeline can read it.
	_, err = os.Stat(filepath.Join(configsDir, "candidate_abc", opt_eval.InstructionFile))
	assert.NoError(t, err)

	// No leftover staging directories.
	entries, err := os.ReadDir(configsDir)
	require.NoError(t, err)
	for _, e := range entries {
		assert.NotContains(t, e.Name(), ".baseline-stage")
	}
}

func TestAdvanceBaselineToCandidate_ArchiveCollisionReplaced(t *testing.T) {
	t.Parallel()

	serviceDir := t.TempDir()
	configsDir := filepath.Join(serviceDir, opt_eval.AgentConfigsDir)

	// A stale archive from a previous deploy of the same job already exists.
	writeAgentConfigDir(t, configsDir, opt_eval.BaselineDir+"_job-1", "stale archive", nil)
	writeAgentConfigDir(t, configsDir, opt_eval.BaselineDir, "current baseline", nil)
	writeAgentConfigDir(t, configsDir, "candidate_abc", "optimized instructions", nil)

	require.NoError(t, advanceBaselineToCandidate(serviceDir, "candidate_abc", "job-1"))

	// The archive is replaced with the baseline that was current at swap time.
	archived, err := os.ReadFile(
		filepath.Join(configsDir, opt_eval.BaselineDir+"_job-1", opt_eval.InstructionFile))
	require.NoError(t, err)
	assert.Equal(t, "current baseline", string(archived))
}

func TestAdvanceBaselineToCandidate_NoJobIDRemovesBaseline(t *testing.T) {
	t.Parallel()

	serviceDir := t.TempDir()
	configsDir := filepath.Join(serviceDir, opt_eval.AgentConfigsDir)
	writeAgentConfigDir(t, configsDir, opt_eval.BaselineDir, "original", nil)
	writeAgentConfigDir(t, configsDir, "candidate_abc", "optimized", nil)

	// An unsafe job ID falls back to removal — no archive is created.
	require.NoError(t, advanceBaselineToCandidate(serviceDir, "candidate_abc", "../evil"))

	got, err := os.ReadFile(filepath.Join(configsDir, opt_eval.BaselineDir, opt_eval.InstructionFile))
	require.NoError(t, err)
	assert.Equal(t, "optimized", string(got))

	entries, err := os.ReadDir(configsDir)
	require.NoError(t, err)
	for _, e := range entries {
		assert.NotContains(t, e.Name(), opt_eval.BaselineDir+"_", "no archive should be created")
	}
}

func TestAdvanceBaselineToCandidate_NoOps(t *testing.T) {
	t.Parallel()

	t.Run("empty service dir", func(t *testing.T) {
		t.Parallel()
		assert.NoError(t, advanceBaselineToCandidate("", "candidate_abc", "job-1"))
	})

	t.Run("empty candidate id", func(t *testing.T) {
		t.Parallel()
		assert.NoError(t, advanceBaselineToCandidate(t.TempDir(), "", "job-1"))
	})

	t.Run("candidate dir missing", func(t *testing.T) {
		t.Parallel()
		serviceDir := t.TempDir()
		// Baseline exists but the candidate does not — leave baseline untouched.
		configsDir := filepath.Join(serviceDir, opt_eval.AgentConfigsDir)
		writeAgentConfigDir(t, configsDir, opt_eval.BaselineDir, "original", nil)

		require.NoError(t, advanceBaselineToCandidate(serviceDir, "candidate_missing", "job-1"))

		got, err := os.ReadFile(filepath.Join(configsDir, opt_eval.BaselineDir, opt_eval.InstructionFile))
		require.NoError(t, err)
		assert.Equal(t, "original", string(got))
	})
}

func TestAdvanceBaselineToCandidate_RejectsTraversal(t *testing.T) {
	t.Parallel()

	for _, id := range []string{"..", ".", filepath.Join("..", "escape"), "a/b"} {
		err := advanceBaselineToCandidate(t.TempDir(), id, "job-1")
		assert.Error(t, err, "candidate id %q should be rejected", id)
	}
}

func TestServiceDirFromProject(t *testing.T) {
	t.Parallel()

	// Empty project path short-circuits before dereferencing svc.
	assert.Equal(t, "", serviceDirFromProject("", nil))
}
