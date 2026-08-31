// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"testing"

	"azureaiagent/internal/pkg/agents/agent_api"

	"github.com/stretchr/testify/require"
)

func TestPromptListQueryFiltersPromptAgents(t *testing.T) {
	query := promptListQuery()
	require.NotNil(t, query.Kind)
	require.Equal(t, agent_api.AgentKindPrompt, *query.Kind)
}
