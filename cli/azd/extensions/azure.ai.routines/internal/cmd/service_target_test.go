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

func TestParseRoutineServiceConfig_ServiceLevel(t *testing.T) {
	t.Parallel()

	props, err := structpb.NewStruct(map[string]any{
		"description": "nightly summary",
		"enabled":     true,
		"triggers": map[string]any{
			"default": map[string]any{"type": "recurring", "cron_expression": "0 9 * * *"},
		},
		"action": map[string]any{"type": "invoke_agent_responses_api", "agent_name": "summarizer"},
	})
	require.NoError(t, err)

	body, err := parseRoutineServiceConfig(&azdext.ServiceConfig{
		Name:                 "nightly",
		Host:                 aiRoutineHost,
		AdditionalProperties: props,
	})
	require.NoError(t, err)
	assert.Equal(t, "nightly summary", body.Description)
	require.NotNil(t, body.Enabled)
	assert.True(t, *body.Enabled)
	require.Contains(t, body.Triggers, "default")
	assert.Equal(t, "recurring", body.Triggers["default"].Type)
	assert.Equal(t, "0 9 * * *", body.Triggers["default"].CronExpression)
	require.NotNil(t, body.Action)
	assert.Equal(t, "summarizer", body.Action.AgentName)
}

// TestParseRoutineServiceConfig_ConfigFallback verifies routines written before
// the per-resource service split (config-nested shape) still parse.
func TestParseRoutineServiceConfig_ConfigFallback(t *testing.T) {
	t.Parallel()

	props, err := structpb.NewStruct(map[string]any{"description": "legacy"})
	require.NoError(t, err)

	body, err := parseRoutineServiceConfig(&azdext.ServiceConfig{
		Name:   "legacy",
		Host:   aiRoutineHost,
		Config: props,
	})
	require.NoError(t, err)
	assert.Equal(t, "legacy", body.Description)
}
