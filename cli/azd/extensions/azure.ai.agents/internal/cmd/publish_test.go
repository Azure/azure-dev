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
		false,
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

func TestWritePublishResultGuidance(t *testing.T) {
	t.Parallel()

	result := &agent_api.TeamsAppPublishResult{
		TitleID:    "T_title",
		TeamsAppID: "app-id",
	}
	deepLink := "https://teams.microsoft.com/v2/#/l/app/?titleId=T_title"

	tests := []struct {
		name             string
		scope            string
		isDigitalWorker  bool
		contains         []string
		notContains      []string
		wantDeepLink     bool
		wantApprovalLink bool
	}{
		{
			name:  "simple shared",
			scope: "shared",
			contains: []string{
				"Published Teams app",
				"Install link: " + deepLink,
				"subject to their tenant's app installation policies",
			},
			notContains:  []string{"create an instance", "Admin approval"},
			wantDeepLink: true,
		},
		{
			name:  "simple tenant",
			scope: "tenant",
			contains: []string{
				"Published Teams app",
				"awaits IT-admin approval",
				"Admin approval: " + tenantAgentApprovalURL,
			},
			notContains:      []string{"Install link", "create instances"},
			wantApprovalLink: true,
		},
		{
			name:            "digital worker shared",
			scope:           "shared",
			isDigitalWorker: true,
			contains: []string{
				"Published Digital Worker",
				"Install link: " + deepLink,
				"may submit an activation request",
				"Admin approval: " + tenantAgentApprovalURL,
				"reopen the install link",
			},
			wantDeepLink:     true,
			wantApprovalLink: true,
		},
		{
			name:            "digital worker tenant",
			scope:           "tenant",
			isDigitalWorker: true,
			contains: []string{
				"Published Digital Worker",
				"may require approval and template activation",
				"Admin approval: " + tenantAgentApprovalURL,
				"create their personal instances",
			},
			notContains:      []string{"Install link"},
			wantApprovalLink: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			scope, err := resolveTeamsPackScope(test.scope)
			require.NoError(t, err)

			var textOutput bytes.Buffer
			require.NoError(t, writePublishResult(
				&textOutput, "", result, scope, "Contoso Helper", "agent-service", deepLink, test.isDigitalWorker,
			))
			for _, expected := range test.contains {
				require.Contains(t, textOutput.String(), expected)
			}
			for _, unexpected := range test.notContains {
				require.NotContains(t, textOutput.String(), unexpected)
			}

			var jsonOutput bytes.Buffer
			require.NoError(t, writePublishResult(
				&jsonOutput, "json", result, scope, "Contoso Helper", "agent-service", deepLink, test.isDigitalWorker,
			))

			var payload map[string]string
			require.NoError(t, json.Unmarshal(jsonOutput.Bytes(), &payload))
			if test.wantDeepLink {
				require.Equal(t, deepLink, payload["deepLink"])
			} else {
				require.NotContains(t, payload, "deepLink")
			}
			if test.wantApprovalLink {
				require.Equal(t, tenantAgentApprovalURL, payload["approvalLink"])
			} else {
				require.NotContains(t, payload, "approvalLink")
			}
		})
	}
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
