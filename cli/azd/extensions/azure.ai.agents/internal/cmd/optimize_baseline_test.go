// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"azureaiagent/internal/pkg/agents/opt_eval"

	azdext "github.com/azure/azure-dev/cli/azd/pkg/azdext"
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

func TestAdvanceBaselineToCandidate_ArchiveCollisionPreserved(t *testing.T) {
	t.Parallel()

	serviceDir := t.TempDir()
	configsDir := filepath.Join(serviceDir, opt_eval.AgentConfigsDir)

	// A stale archive from a previous deploy of the same job already exists.
	writeAgentConfigDir(t, configsDir, opt_eval.BaselineDir+"_job-1", "stale archive", nil)
	writeAgentConfigDir(t, configsDir, opt_eval.BaselineDir, "current baseline", nil)
	writeAgentConfigDir(t, configsDir, "candidate_abc", "optimized instructions", nil)

	require.NoError(t, advanceBaselineToCandidate(serviceDir, "candidate_abc", "job-1"))

	// The first archive for the job remains the original rollback snapshot.
	archived, err := os.ReadFile(
		filepath.Join(configsDir, opt_eval.BaselineDir+"_job-1", opt_eval.InstructionFile))
	require.NoError(t, err)
	assert.Equal(t, "stale archive", string(archived))

	baseline, err := os.ReadFile(
		filepath.Join(configsDir, opt_eval.BaselineDir, opt_eval.InstructionFile))
	require.NoError(t, err)
	assert.Equal(t, "optimized instructions", string(baseline))
}

func TestAdvanceBaselineToCandidate_ArchiveCollisionRestoresCurrentBaselineOnPromotionFailure(t *testing.T) {
	t.Parallel()

	serviceDir := t.TempDir()
	configsDir := filepath.Join(serviceDir, opt_eval.AgentConfigsDir)
	writeAgentConfigDir(t, configsDir, opt_eval.BaselineDir+"_job-1", "original baseline", nil)
	writeAgentConfigDir(t, configsDir, opt_eval.BaselineDir, "current baseline", nil)
	writeAgentConfigDir(t, configsDir, "candidate_abc", "optimized", nil)

	rename := func(oldPath, newPath string) error {
		if strings.Contains(filepath.Base(oldPath), ".baseline-stage-") &&
			newPath == filepath.Join(configsDir, opt_eval.BaselineDir) {
			return assert.AnError
		}
		return os.Rename(oldPath, newPath)
	}

	err := advanceBaselineToCandidateWithRename(serviceDir, "candidate_abc", "job-1", rename)
	require.ErrorIs(t, err, assert.AnError)

	baseline, readErr := os.ReadFile(
		filepath.Join(configsDir, opt_eval.BaselineDir, opt_eval.InstructionFile))
	require.NoError(t, readErr)
	assert.Equal(t, "current baseline", string(baseline))

	archive, readErr := os.ReadFile(
		filepath.Join(configsDir, opt_eval.BaselineDir+"_job-1", opt_eval.InstructionFile))
	require.NoError(t, readErr)
	assert.Equal(t, "original baseline", string(archive))
}

func TestAdvanceBaselineToCandidate_RestoresBaselineWhenPromotionFails(t *testing.T) {
	t.Parallel()

	serviceDir := t.TempDir()
	configsDir := filepath.Join(serviceDir, opt_eval.AgentConfigsDir)
	writeAgentConfigDir(t, configsDir, opt_eval.BaselineDir, "original", nil)
	writeAgentConfigDir(t, configsDir, "candidate_abc", "optimized", nil)

	promotionFailed := false
	rename := func(oldPath, newPath string) error {
		if filepath.Base(oldPath) != opt_eval.BaselineDir &&
			newPath == filepath.Join(configsDir, opt_eval.BaselineDir) &&
			!promotionFailed {
			promotionFailed = true
			return assert.AnError
		}
		return os.Rename(oldPath, newPath)
	}

	err := advanceBaselineToCandidateWithRename(
		serviceDir,
		"candidate_abc",
		"job-1",
		rename,
	)
	require.ErrorIs(t, err, assert.AnError)

	baseline, readErr := os.ReadFile(
		filepath.Join(configsDir, opt_eval.BaselineDir, opt_eval.InstructionFile))
	require.NoError(t, readErr)
	assert.Equal(t, "original", string(baseline))
	assert.NoDirExists(t, filepath.Join(configsDir, opt_eval.BaselineDir+"_job-1"))
}

func TestAdvanceBaselineToCandidate_RemovesPartialBaselineWhenRestoreFails(t *testing.T) {
	t.Parallel()

	serviceDir := t.TempDir()
	configsDir := filepath.Join(serviceDir, opt_eval.AgentConfigsDir)
	writeAgentConfigDir(t, configsDir, opt_eval.BaselineDir, "original", nil)
	writeAgentConfigDir(t, configsDir, "candidate_abc", "optimized", nil)

	promotionFailed := false
	rename := func(oldPath, newPath string) error {
		if newPath == filepath.Join(configsDir, opt_eval.BaselineDir) {
			if !promotionFailed {
				promotionFailed = true
				return assert.AnError
			}
			if strings.Contains(filepath.Base(oldPath), ".baseline-rollback-") {
				return errors.New("restore rename failed")
			}
		}
		return os.Rename(oldPath, newPath)
	}
	restoreErr := errors.New("restore copy failed")
	copyDir := func(src, dst string) error {
		if strings.Contains(filepath.Base(src), ".baseline-rollback-") {
			require.NoError(t, os.MkdirAll(dst, 0750))
			require.NoError(t, os.WriteFile(filepath.Join(dst, "partial"), []byte("partial"), 0600))
			return restoreErr
		}
		return copyDirectory(src, dst)
	}

	err := advanceBaselineToCandidateWithOps(
		serviceDir,
		"candidate_abc",
		"job-1",
		rename,
		copyDir,
	)
	require.ErrorIs(t, err, assert.AnError)
	require.ErrorContains(t, err, "restoring rollback: restore rename failed")
	require.ErrorContains(t, err, "copying rollback: restore copy failed")
	require.ErrorContains(t, err, "partial baseline removed")
	assert.NoDirExists(t, filepath.Join(configsDir, opt_eval.BaselineDir))
	assert.NoDirExists(t, filepath.Join(configsDir, opt_eval.BaselineDir+"_job-1"))
	assert.DirExists(t, filepath.Join(configsDir, "candidate_abc"))

	entries, readErr := os.ReadDir(configsDir)
	require.NoError(t, readErr)
	assert.Condition(t, func() bool {
		for _, entry := range entries {
			if strings.Contains(entry.Name(), ".baseline-rollback-") {
				return true
			}
		}
		return false
	}, "the exact previous baseline should remain in the rollback directory")
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

	t.Run("candidate path is a file", func(t *testing.T) {
		t.Parallel()
		serviceDir := t.TempDir()
		configsDir := filepath.Join(serviceDir, opt_eval.AgentConfigsDir)
		require.NoError(t, os.MkdirAll(configsDir, 0750))
		require.NoError(t, os.WriteFile(
			filepath.Join(configsDir, "candidate_file"),
			[]byte("not a directory"),
			0600,
		))

		err := advanceBaselineToCandidate(serviceDir, "candidate_file", "job-1")
		require.ErrorContains(t, err, "is not a directory")
	})
}

func TestAdvanceBaselineToCandidate_RejectsTraversal(t *testing.T) {
	t.Parallel()

	for _, id := range []string{"..", ".", filepath.Join("..", "escape"), "a/b"} {
		err := advanceBaselineToCandidate(t.TempDir(), id, "job-1")
		assert.Error(t, err, "candidate id %q should be rejected", id)
	}
}

func TestCandidateConfigExists_AccessFailure(t *testing.T) {
	t.Parallel()

	accessErr := errors.New("access denied")
	exists, err := candidateConfigExists("candidate", func(string) (os.FileInfo, error) {
		return nil, accessErr
	})

	require.False(t, exists)
	require.ErrorIs(t, err, accessErr)
	require.ErrorContains(t, err, "accessing candidate config")
}

func TestBaselineAdvancementDir(t *testing.T) {
	t.Parallel()

	// Empty project path short-circuits (no local project on disk).
	assert.Equal(t, "", baselineAdvancementDir("", &azdext.ServiceConfig{Name: "a"}))

	root := t.TempDir()

	// A unique service resolves to its directory under the project root.
	svcA := &azdext.ServiceConfig{Name: "a", RelativePath: "svc-a"}
	assert.Equal(t, filepath.Join(root, "svc-a"), baselineAdvancementDir(root, svcA))

	// A traversing RelativePath escapes the root and is skipped.
	svcEscape := &azdext.ServiceConfig{Name: "a", RelativePath: "../outside"}
	assert.Equal(t, "", baselineAdvancementDir(root, svcEscape))
}
