// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"azureaiagent/internal/pkg/agents/agent_api"

	"github.com/stretchr/testify/require"
)

func TestWritePublishResultJson(t *testing.T) {
	t.Parallel()

	scope, err := resolveTeamsPackScope("shared")
	require.NoError(t, err)

	var stdout bytes.Buffer
	err = writePublishResult(
		&stdout,
		"json",
		&agent_api.TeamsAppPublishResult{
			TitleID:    "T_title",
			TeamsAppID: "app-id",
		},
		scope,
		"Contoso Helper",
		"agent-service",
		"https://teams.microsoft.com/v2/#/l/app/?titleId=T_title",
	)
	require.NoError(t, err)

	var payload map[string]string
	require.NoError(t, json.Unmarshal(stdout.Bytes(), &payload))
	require.Equal(t, map[string]string{
		"titleId":     "T_title",
		"teamsAppId":  "app-id",
		"scope":       "shared",
		"displayName": "Contoso Helper",
		"deepLink":    "https://teams.microsoft.com/v2/#/l/app/?titleId=T_title",
	}, payload)
	require.NotContains(t, stdout.String(), "Publishing Teams app")
	require.False(t, strings.Contains(stdout.String(), "Published Teams app"))
}
