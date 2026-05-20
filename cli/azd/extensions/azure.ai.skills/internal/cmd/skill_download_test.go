// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"azureaiskills/internal/exterrors"
	"azureaiskills/internal/pkg/skill_api"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/require"
)

func TestArchiveExtension(t *testing.T) {
	cases := []struct {
		format skill_api.ArchiveFormat
		want   string
	}{
		{skill_api.ArchiveZip, ".zip"},
		{skill_api.ArchiveTarGz, ".tar.gz"},
		{skill_api.ArchiveUnknown, ".bin"},
	}
	for _, c := range cases {
		require.Equal(t, c.want, archiveExtension(c.format), "format=%v", c.format)
	}
}

func TestClassifyExtractError_UnsafeEntry(t *testing.T) {
	wrapped := fmt.Errorf("entry escapes: %w", skill_api.ErrUnsafeEntry)
	err := classifyExtractError(wrapped, "/tmp/out")
	require.Error(t, err)
	var le *azdext.LocalError
	require.True(t, errors.As(err, &le))
	require.Equal(t, exterrors.CodeSkillArchiveUnsafe, le.Code)
}

func TestClassifyExtractError_LimitExceeded(t *testing.T) {
	wrapped := fmt.Errorf("too big: %w", skill_api.ErrLimitExceeded)
	err := classifyExtractError(wrapped, "/tmp/out")
	require.Error(t, err)
	var le *azdext.LocalError
	require.True(t, errors.As(err, &le))
	require.Equal(t, exterrors.CodeSkillArchiveUnsafe, le.Code)
}

func TestClassifyExtractError_Collision(t *testing.T) {
	wrapped := fmt.Errorf("collision: %w", skill_api.ErrCollision)
	err := classifyExtractError(wrapped, "/tmp/out")
	require.Error(t, err)
	var le *azdext.LocalError
	require.True(t, errors.As(err, &le))
	require.Equal(t, exterrors.CodeSkillOutputCollision, le.Code)
	require.Contains(t, le.Suggestion, "/tmp/out", "suggestion should mention output dir")
}

func TestClassifyExtractError_InvalidArchive(t *testing.T) {
	wrapped := fmt.Errorf("bad header: %w", skill_api.ErrInvalidArchive)
	err := classifyExtractError(wrapped, "/tmp/out")
	require.Error(t, err)
	var le *azdext.LocalError
	require.True(t, errors.As(err, &le))
	require.Equal(t, exterrors.CodeInvalidParameter, le.Code)
}

func TestClassifyExtractError_UnknownPassthrough(t *testing.T) {
	original := errors.New("io: short read")
	err := classifyExtractError(original, "/tmp/out")
	require.Same(t, original, err, "unknown errors must propagate unwrapped")
}

func TestDownloadAction_RejectsInvalidName(t *testing.T) {
	a := &downloadAction{flags: &downloadFlags{name: "bad name"}}
	err := a.Run(context.Background())
	require.Error(t, err)
	var le *azdext.LocalError
	require.True(t, errors.As(err, &le))
	require.Equal(t, exterrors.CodeInvalidSkillName, le.Code)
}

func TestDownloadAction_WriteSkillMd(t *testing.T) {
	dir := t.TempDir()
	a := &downloadAction{flags: &downloadFlags{name: "my-skill", output: outputJSON}}
	skill := &skill_api.Skill{
		Name:         "my-skill",
		Description:  "Greets the user",
		Metadata:     map[string]string{"owner": "alice"},
		Instructions: "# Body\n\nGreet warmly.\n",
	}

	require.NoError(t, a.writeSkillMd(skill, dir))

	got, err := os.ReadFile(filepath.Join(dir, skill_api.SkillMdFileName))
	require.NoError(t, err)
	parsed, err := skill_api.ParseSkillMd(got)
	require.NoError(t, err)
	require.Equal(t, "my-skill", parsed.Name)
	require.Equal(t, "Greets the user", parsed.Description)
	require.Equal(t, map[string]string{"owner": "alice"}, parsed.Metadata)
	require.Equal(t, "# Body\n\nGreet warmly.\n", parsed.Instructions)
}

func TestDownloadAction_WriteSkillMd_CollisionWithoutForce(t *testing.T) {
	dir := t.TempDir()
	skillMdPath := filepath.Join(dir, skill_api.SkillMdFileName)
	require.NoError(t, os.WriteFile(skillMdPath, []byte("old"), 0600))

	a := &downloadAction{flags: &downloadFlags{name: "my-skill", output: outputJSON}}
	err := a.writeSkillMd(&skill_api.Skill{Name: "my-skill"}, dir)
	require.Error(t, err)
	var le *azdext.LocalError
	require.True(t, errors.As(err, &le))
	require.Equal(t, exterrors.CodeSkillOutputCollision, le.Code)
}

func TestDownloadAction_WriteSkillMd_ForceOverwrite(t *testing.T) {
	dir := t.TempDir()
	skillMdPath := filepath.Join(dir, skill_api.SkillMdFileName)
	require.NoError(t, os.WriteFile(skillMdPath, []byte("old"), 0600))

	a := &downloadAction{flags: &downloadFlags{name: "my-skill", output: outputJSON, force: true}}
	require.NoError(t, a.writeSkillMd(&skill_api.Skill{Name: "my-skill", Description: "new"}, dir))

	got, err := os.ReadFile(skillMdPath)
	require.NoError(t, err)
	require.NotEqual(t, "old", string(got), "force should overwrite existing file")
	require.Contains(t, string(got), "name: my-skill")
}
