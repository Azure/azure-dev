// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/structpb"
)

func TestParseSkillServiceConfig_ServiceLevel(t *testing.T) {
	t.Parallel()

	props, err := structpb.NewStruct(map[string]any{
		"description":  "code review skill",
		"instructions": "Review code for correctness.",
		"tools":        []any{"code_interpreter"},
	})
	require.NoError(t, err)

	cfg, err := parseSkillServiceConfig(&azdext.ServiceConfig{
		Name:                 "code-review",
		Host:                 aiSkillHost,
		AdditionalProperties: props,
	})
	require.NoError(t, err)
	assert.Equal(t, "code review skill", cfg.Description)
	assert.Equal(t, "Review code for correctness.", cfg.Instructions)
	assert.Equal(t, []string{"code_interpreter"}, cfg.Tools)
}

// TestParseSkillServiceConfig_ConfigFallback verifies skills written before the
// per-resource service split (config-nested shape) still parse.
func TestParseSkillServiceConfig_ConfigFallback(t *testing.T) {
	t.Parallel()

	props, err := structpb.NewStruct(map[string]any{
		"instructions": "legacy shape",
	})
	require.NoError(t, err)

	cfg, err := parseSkillServiceConfig(&azdext.ServiceConfig{
		Name:   "legacy",
		Host:   aiSkillHost,
		Config: props,
	})
	require.NoError(t, err)
	assert.Equal(t, "legacy shape", cfg.Instructions)
}

func TestParseSkillServiceConfig_Empty(t *testing.T) {
	t.Parallel()

	cfg, err := parseSkillServiceConfig(&azdext.ServiceConfig{Name: "empty", Host: aiSkillHost})
	require.NoError(t, err)
	assert.Empty(t, cfg.Instructions)
}

func TestResolveSkillInstructions_Inline(t *testing.T) {
	t.Parallel()

	got, err := resolveSkillInstructions(&azdext.ServiceConfig{Name: "inline"}, "Review code for correctness.")
	require.NoError(t, err)
	assert.Equal(t, "Review code for correctness.", got)
}

func TestResolveSkillInstructions_FilePath(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "instructions.md"), []byte("Review from file."), 0600))

	got, err := resolveSkillInstructions(
		&azdext.ServiceConfig{Name: "file", RelativePath: dir},
		"instructions.md",
	)
	require.NoError(t, err)
	assert.Equal(t, "Review from file.", got)
}

// TestResolveSkillInstructions_PathTraversal verifies a relative instructions
// path that tries to escape the service directory with ".." is rejected rather
// than read from disk.
func TestResolveSkillInstructions_PathTraversal(t *testing.T) {
	t.Parallel()

	for _, instructions := range []string{"../secret.md", "../../etc/passwd.txt", "sub/../../escape.md"} {
		_, err := resolveSkillInstructions(
			&azdext.ServiceConfig{Name: "traversal", RelativePath: t.TempDir()},
			instructions,
		)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "must not contain '..'")
	}
}
