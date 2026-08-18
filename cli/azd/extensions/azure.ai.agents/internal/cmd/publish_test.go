// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"azureaiagent/internal/pkg/agents/agent_api"
	"azureaiagent/internal/project"

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

func TestWritePublishResultTenantOmitsUnavailableInstallLink(t *testing.T) {
	t.Parallel()

	scope, err := resolveTeamsPackScope("tenant")
	require.NoError(t, err)

	result := &agent_api.TeamsAppPublishResult{
		TitleID:    "T_title",
		TeamsAppID: "app-id",
	}
	deepLink := "https://teams.microsoft.com/v2/#/l/app/?titleId=T_title"

	var textOutput bytes.Buffer
	require.NoError(t, writePublishResult(
		&textOutput, "", result, scope, "Contoso Helper", "agent-service", deepLink,
	))
	require.NotContains(t, textOutput.String(), "Install link")
	require.NotContains(t, textOutput.String(), deepLink)
	require.Contains(t, textOutput.String(), "awaits IT-admin approval")
	require.Contains(t, textOutput.String(), "Admin approval: "+tenantAgentApprovalURL)

	var jsonOutput bytes.Buffer
	require.NoError(t, writePublishResult(
		&jsonOutput, "json", result, scope, "Contoso Helper", "agent-service", deepLink,
	))

	var payload map[string]string
	require.NoError(t, json.Unmarshal(jsonOutput.Bytes(), &payload))
	require.NotContains(t, payload, "deepLink")
	require.Equal(t, tenantAgentApprovalURL, payload["approvalLink"])
}

func TestResolvePublishScopeUsesDigitalWorkerConfigWhenFlagOmitted(t *testing.T) {
	t.Parallel()

	scope, err := resolvePublishScope(&publishFlags{}, digitalWorkerPackContext("tenant"))

	require.NoError(t, err)
	require.Equal(t, "tenant", scope.flag)
}

func TestResolvePublishScopeDefaultsSimpleActivityToShared(t *testing.T) {
	t.Parallel()

	scope, err := resolvePublishScope(&publishFlags{}, &teamsPackContext{
		activityProfile: project.ActivityProfile{UseCase: project.ActivityUseCaseSimple},
	})

	require.NoError(t, err)
	require.Equal(t, "shared", scope.flag)
}

func TestResolvePublishScopeExplicitFlagOverridesDigitalWorkerConfig(t *testing.T) {
	t.Parallel()

	scope, err := resolvePublishScope(
		&publishFlags{scope: "shared", scopeSet: true},
		digitalWorkerPackContext("tenant"),
	)

	require.NoError(t, err)
	require.Equal(t, "shared", scope.flag)
}

func digitalWorkerPackContext(publishScope string) *teamsPackContext {
	return &teamsPackContext{
		activityProfile: project.ActivityProfile{UseCase: project.ActivityUseCaseDigitalWorker},
		activitySettings: &project.ActivitySettings{
			Publish: &project.DigitalWorkerPublishConfig{PublishScope: publishScope},
		},
	}
}
