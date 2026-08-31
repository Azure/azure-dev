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
			wantDeepLink:     true,
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

func TestResolvePublishScopeUsesSimpleActivityConfigWhenFlagOmitted(t *testing.T) {
	t.Parallel()

	packCtx := activityPackContext(project.ActivityUseCaseSimple, "tenant")
	scope, err := resolvePublishScope(&publishFlags{}, packCtx)

	require.NoError(t, err)
	require.Equal(t, "tenant", scope.flag)
}

func TestResolvePublishScopeExplicitFlagOverridesDigitalWorkerConfig(t *testing.T) {
	t.Parallel()

	scope, err := resolvePublishScope(
		&publishFlags{scope: "tenant", scopeSet: true},
		digitalWorkerPackContext("shared"),
	)

	require.NoError(t, err)
	require.Equal(t, "tenant", scope.flag)
}

func TestResolvePublishScopeRejectsSharedForDigitalWorkerFromFlag(t *testing.T) {
	t.Parallel()

	_, err := resolvePublishScope(
		&publishFlags{scope: "shared", scopeSet: true},
		digitalWorkerPackContext("tenant"),
	)

	require.Error(t, err)
	require.Contains(t, err.Error(), "digital_worker publish does not support \"shared\" scope")
}

func TestResolvePublishScopeRejectsSharedForDigitalWorkerFromConfig(t *testing.T) {
	t.Parallel()

	_, err := resolvePublishScope(&publishFlags{}, digitalWorkerPackContext("shared"))

	require.Error(t, err)
	require.Contains(t, err.Error(), "digital_worker publish does not support \"shared\" scope")
}

func TestResolvePublishScopeDefaultsDigitalWorkerToTenantWhenConfigMissing(t *testing.T) {
	t.Parallel()

	scope, err := resolvePublishScope(&publishFlags{}, &teamsPackContext{
		activityProfile:  project.ActivityProfile{UseCase: project.ActivityUseCaseDigitalWorker},
		activitySettings: &project.ActivitySettings{},
	})

	require.NoError(t, err)
	require.Equal(t, "tenant", scope.flag)
}

func TestBuildTeamsAppPackageRequestPublishMetadataPrecedence(t *testing.T) {
	t.Parallel()

	publish := &project.ActivityPublishConfig{
		AppVersion:       "2.3.4",
		AgentDisplayName: "Configured Digital Worker",
	}

	configured := buildTeamsAppPackageRequest("", teamsAppRequestOptions{publish: publish})
	require.Equal(t, "2.3.4", configured.AppVersion)
	require.Equal(t, "Configured Digital Worker", configured.AgentDisplayName)

	explicit := buildTeamsAppPackageRequest("", teamsAppRequestOptions{
		displayName: "Flag Digital Worker",
		appVersion:  "9.8.7",
		publish:     publish,
	})
	require.Equal(t, "9.8.7", explicit.AppVersion)
	require.Equal(t, "Flag Digital Worker", explicit.AgentDisplayName)
}

func TestBuildTeamsAppPackageRequestUsesPublishMetadataForSimpleActivity(t *testing.T) {
	t.Parallel()

	configured := buildTeamsAppPackageRequest("", teamsAppRequestOptions{
		publish: activityPublishConfig(activityPackContext(project.ActivityUseCaseSimple, "shared")),
	})
	require.Equal(t, "2.3.4", configured.AppVersion)
	require.Equal(t, "Configured Activity agent", configured.AgentDisplayName)
	require.Equal(t, "Shared", configured.PublishScope)
	require.False(t, configured.PublishAsAutopilot)
	require.Empty(t, configured.OptionalPermissionScopes)
	require.Nil(t, configured.AccessBoundaries)

	explicit := buildTeamsAppPackageRequest("", teamsAppRequestOptions{
		displayName: "CLI Activity agent",
		appVersion:  "9.8.7",
		scope:       teamsPackScope{flag: "tenant", api: "Tenant"},
		publish:     activityPublishConfig(activityPackContext(project.ActivityUseCaseSimple, "shared")),
	})
	require.Equal(t, "9.8.7", explicit.AppVersion)
	require.Equal(t, "CLI Activity agent", explicit.AgentDisplayName)
	require.Equal(t, "Tenant", explicit.PublishScope)
	require.False(t, explicit.PublishAsAutopilot)
	require.Empty(t, explicit.OptionalPermissionScopes)
	require.Nil(t, explicit.AccessBoundaries)
}

func TestParseOptionalPermissionScopeFlags(t *testing.T) {
	t.Parallel()

	got, err := parseOptionalPermissionScopeFlags([]string{
		"resource-a=McpServers.Mail.All",
		"resource-b=Ado.Mcp.Tools",
		"resource-a=McpServers.Calendar.All",
		"resource-a=McpServers.Mail.All",
	})
	require.NoError(t, err)
	require.Equal(t, []agent_api.Microsoft365PermissionScopes{
		{ResourceAppID: "resource-a", Scopes: []string{"McpServers.Mail.All", "McpServers.Calendar.All"}},
		{ResourceAppID: "resource-b", Scopes: []string{"Ado.Mcp.Tools"}},
	}, got)
}

func TestParseOptionalPermissionScopeFlagsRejectsInvalidValue(t *testing.T) {
	t.Parallel()

	_, err := parseOptionalPermissionScopeFlags([]string{"McpServers.Mail.All"})
	require.ErrorContains(t, err, "invalid optional permission scope")
}

func TestResolveDigitalWorkerPublishInputs(t *testing.T) {
	t.Parallel()

	configBoundaries := []string{"read.1on1.developers"}
	packCtx := digitalWorkerPackContext("tenant")
	packCtx.activitySettings.Publish.OptionalPermissionScopes = []project.Microsoft365PermissionScopes{
		{ResourceAppID: "configured-app", Scopes: []string{"Configured.Scope"}},
	}
	packCtx.activitySettings.Publish.AccessBoundaries = &configBoundaries

	permissions, boundaries, err := resolveDigitalWorkerPublishInputs(&publishFlags{}, packCtx)
	require.NoError(t, err)
	require.Equal(t, "configured-app", permissions[0].ResourceAppID)
	require.Equal(t, configBoundaries, *boundaries)

	permissions, boundaries, err = resolveDigitalWorkerPublishInputs(&publishFlags{
		optionalPermissionScopes:    []string{"flag-app=Flag.Scope"},
		optionalPermissionScopesSet: true,
		accessBoundaries:            []string{"write.group.developers"},
		accessBoundariesSet:         true,
	}, packCtx)
	require.NoError(t, err)
	require.Equal(t, "flag-app", permissions[0].ResourceAppID)
	require.Equal(t, []string{"write.group.developers"}, *boundaries)

	_, boundaries, err = resolveDigitalWorkerPublishInputs(&publishFlags{
		clearAccessBoundaries: true,
	}, packCtx)
	require.NoError(t, err)
	require.NotNil(t, boundaries)
	require.Empty(t, *boundaries)
}

func TestResolveDigitalWorkerPublishInputsRejectsSimpleAgentFlags(t *testing.T) {
	t.Parallel()

	_, _, err := resolveDigitalWorkerPublishInputs(&publishFlags{
		accessBoundaries:    []string{"read.1on1.developers"},
		accessBoundariesSet: true,
	}, activityPackContext(project.ActivityUseCaseSimple, "shared"))
	require.ErrorContains(t, err, "cannot be used for a simple Activity agent")
}

func digitalWorkerPackContext(publishScope string) *teamsPackContext {
	return activityPackContext(project.ActivityUseCaseDigitalWorker, publishScope)
}

func activityPackContext(useCase project.ActivityUseCase, publishScope string) *teamsPackContext {
	return &teamsPackContext{
		activityProfile: project.ActivityProfile{UseCase: useCase},
		activitySettings: &project.ActivitySettings{
			Publish: &project.ActivityPublishConfig{
				PublishScope:     publishScope,
				AppVersion:       "2.3.4",
				AgentDisplayName: "Configured Activity agent",
			},
		},
	}
}
