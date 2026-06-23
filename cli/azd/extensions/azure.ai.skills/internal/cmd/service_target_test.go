// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
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
