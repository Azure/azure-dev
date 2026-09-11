// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"azureaiagent/internal/exterrors"
	"azureaiagent/internal/pkg/agents/insights_api"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type fakeInsightsClient struct {
	monitorPage *insights_api.MonitorPage
	monitorErr  error
	pages       map[string]*insights_api.InsightPage
	pageErr     error
	options     []insights_api.ListInsightOptions
}

func (f *fakeInsightsClient) ListMonitors(
	context.Context,
	string,
	string,
	int,
) (*insights_api.MonitorPage, error) {
	return f.monitorPage, f.monitorErr
}

func (f *fakeInsightsClient) ListInsights(
	_ context.Context,
	_ string,
	options insights_api.ListInsightOptions,
) (*insights_api.InsightPage, error) {
	f.options = append(f.options, options)
	if f.pageErr != nil {
		return nil, f.pageErr
	}
	page, ok := f.pages[options.After]
	if !ok {
		return nil, fmt.Errorf("unexpected cursor %q", options.After)
	}
	return page, nil
}

func TestInsightsExportCommandArguments(t *testing.T) {
	cmd := newInsightsExportCommand(nil)

	assert.NoError(t, cmd.Args(cmd, nil))
	assert.NoError(t, cmd.Args(cmd, []string{"agent"}))
	assert.Error(t, cmd.Args(cmd, []string{"agent", "extra"}))
	assertOutputFlagOptions(t, cmd, "json", []string{"json"})
}

func TestInsightsCommandIncludesExport(t *testing.T) {
	cmd := newInsightsCommand(nil)

	export, _, err := cmd.Find([]string{"export"})

	require.NoError(t, err)
	assert.Equal(t, "export [name]", export.Use)
}

func TestValidateInsightsExportFlagsNormalizesEnums(t *testing.T) {
	flags := &insightsExportFlags{
		category: " aviation ",
		severity: " HIGH ",
		status:   "Resolved",
	}

	err := validateInsightsExportFlags(flags)

	require.NoError(t, err)
	assert.Equal(t, "aviation", flags.category)
	assert.Equal(t, "high", flags.severity)
	assert.Equal(t, "resolved", flags.status)
}

func TestValidateInsightsExportFlagsRejectsUnsupportedValue(t *testing.T) {
	flags := &insightsExportFlags{severity: "critical"}

	err := validateInsightsExportFlags(flags)

	var localErr *azdext.LocalError
	require.ErrorAs(t, err, &localErr)
	assert.Equal(t, "invalid_parameter", localErr.Code)
}

func TestResolveInsightsAgentInfoUsesExplicitAgentOutsideProject(t *testing.T) {
	azdClient := newHelpersTestAzdClient(
		t,
		&helpersProjectServer{err: status.Error(codes.NotFound, "project not found")},
		&helpersPromptServer{},
	)
	flags := &insightsExportFlags{
		name:            "remote-agent",
		projectEndpoint: "https://account.services.ai.azure.com/api/projects/project",
	}

	info, err := resolveInsightsAgentInfo(t.Context(), azdClient, flags, true)

	require.NoError(t, err)
	assert.Equal(t, "remote-agent", info.AgentName)
}

func TestResolveInsightsAgentInfoUsesServiceFromRequestedEnvironment(t *testing.T) {
	projectServer := &helpersProjectServer{project: &azdext.ProjectConfig{
		Path: t.TempDir(),
		Services: map[string]*azdext.ServiceConfig{
			"service-key": {
				Name: "service-key",
				Host: AiAgentHost,
			},
		},
	}}
	envServer := &testEnvironmentServiceServer{
		current: &azdext.Environment{Name: "dev"},
		environments: map[string]*azdext.Environment{
			"staging": {Name: "staging"},
		},
		values: map[string]map[string]string{
			"dev": {
				"AGENT_SERVICE_KEY_NAME": "dev-agent",
			},
			"staging": {
				"AGENT_SERVICE_KEY_NAME": "staging-agent",
			},
		},
	}
	azdClient := newHelpersTestAzdClient(
		t,
		projectServer,
		&helpersPromptServer{},
		envServer,
	)
	flags := &insightsExportFlags{
		name:            "service-key",
		envName:         "staging",
		projectEndpoint: "https://account.services.ai.azure.com/api/projects/project",
	}

	info, err := resolveInsightsAgentInfo(t.Context(), azdClient, flags, true)

	require.NoError(t, err)
	assert.Equal(t, "staging-agent", info.AgentName)
}

func TestResolveInsightsProjectEndpointBindsEnvironment(t *testing.T) {
	for _, tc := range []struct {
		name        string
		envName     string
		serviceName string
	}{
		{name: "explicit environment", envName: "staging"},
		{name: "current project environment", serviceName: "service-key"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("FOUNDRY_PROJECT_ENDPOINT", "https://shell.services.ai.azure.com/api/projects/other")
			original := readAzdHostedSourcesFunc
			readAzdHostedSourcesFunc = func(
				_ context.Context, opts resolveProjectEndpointOpts,
			) (azdHostedSources, error) {
				assert.True(t, opts.RequireEnvironmentEndpoint)
				assert.Equal(t, tc.envName, opts.EnvName)
				return azdHostedSources{
					CfgFound: true,
					CfgState: projectContextState{Endpoint: "https://global.services.ai.azure.com/api/projects/other"},
				}, nil
			}
			t.Cleanup(func() { readAzdHostedSourcesFunc = original })

			resolved, err := resolveInsightsProjectEndpoint(t.Context(),
				&insightsExportFlags{envName: tc.envName}, &AgentServiceInfo{ServiceName: tc.serviceName})

			require.Nil(t, resolved)
			localErr, ok := errors.AsType[*azdext.LocalError](err)
			require.True(t, ok)
			assert.Equal(t, exterrors.CodeMissingProjectEndpoint, localErr.Code)
			assert.Contains(t, localErr.Suggestion, "--project-endpoint")
			assert.NotContains(t, localErr.Suggestion, "azd ai project set")
		})
	}
}

func TestResolveInsightsProjectEndpointExplicitOverride(t *testing.T) {
	stubAzdHostedSources(t, azdHostedSources{}, errors.New("environment must not be read"))
	endpoint := "https://explicit.services.ai.azure.com/api/projects/project"

	resolved, err := resolveInsightsProjectEndpoint(t.Context(),
		&insightsExportFlags{envName: "staging", projectEndpoint: endpoint},
		&AgentServiceInfo{ServiceName: "service-key"})

	require.NoError(t, err)
	assert.Equal(t, endpoint, resolved.Endpoint)
	assert.Equal(t, SourceFlag, resolved.Source)
}

func TestResolveInsightsProjectEndpointStandaloneFallback(t *testing.T) {
	stubAzdHostedSources(t, azdHostedSources{
		CfgFound: true,
		CfgState: projectContextState{Endpoint: "https://global.services.ai.azure.com/api/projects/project"},
	}, nil)

	resolved, err := resolveInsightsProjectEndpoint(t.Context(),
		&insightsExportFlags{}, &AgentServiceInfo{AgentName: "remote-agent"})

	require.NoError(t, err)
	assert.Equal(t, SourceGlobalConfig, resolved.Source)
}

func TestInsightsExportActionWritesAllPagesToStdout(t *testing.T) {
	client := &fakeInsightsClient{
		monitorPage: &insights_api.MonitorPage{
			Data: []insights_api.Monitor{{ID: "monitor-1", AgentName: "Agent"}},
		},
		pages: map[string]*insights_api.InsightPage{
			"": {
				Data:    []json.RawMessage{json.RawMessage(`{"id":"insight-1","extra":"kept"}`)},
				LastID:  "insight-1",
				HasMore: true,
			},
			"insight-1": {
				Data: []json.RawMessage{json.RawMessage(`{"id":"insight-2"}`)},
			},
		},
	}
	var output bytes.Buffer
	action := &insightsExportAction{
		client:          client,
		writer:          &output,
		now:             func() time.Time { return time.Date(2026, 9, 9, 20, 0, 0, 0, time.UTC) },
		projectEndpoint: "https://account.services.ai.azure.com/api/projects/project",
		flags: &insightsExportFlags{
			name:           "agent",
			category:       "aviation",
			severity:       "high",
			status:         "active",
			includeDetails: true,
		},
	}

	err := action.Run(t.Context())

	require.NoError(t, err)
	var document insightsExportDocument
	require.NoError(t, json.Unmarshal(output.Bytes(), &document))
	assert.Equal(t, "v1", document.APIVersion)
	assert.Equal(t, "2026-09-09T20:00:00Z", document.ExportedAt)
	assert.Equal(t, "Agent", document.AgentName)
	assert.Equal(t, "monitor-1", document.MonitorID)
	assert.Equal(t, 2, document.InsightCount)
	require.Len(t, document.Insights, 2)
	assert.JSONEq(t, `{"id":"insight-1","extra":"kept"}`, string(document.Insights[0]))
	require.Len(t, client.options, 2)
	assert.Empty(t, client.options[0].After)
	assert.Equal(t, "insight-1", client.options[1].After)
	assert.True(t, client.options[0].IncludeDetails)
}

func TestInsightsExportActionWritesFile(t *testing.T) {
	outFile := filepath.Join(t.TempDir(), "nested", "insights.json")
	client := &fakeInsightsClient{
		monitorPage: &insights_api.MonitorPage{
			Data: []insights_api.Monitor{{ID: "monitor-1", AgentName: "agent"}},
		},
		pages: map[string]*insights_api.InsightPage{"": {}},
	}
	action := &insightsExportAction{
		client:          client,
		writer:          &bytes.Buffer{},
		now:             func() time.Time { return time.Unix(0, 0) },
		projectEndpoint: "https://account.services.ai.azure.com/api/projects/project",
		flags: &insightsExportFlags{
			name:           "agent",
			outFile:        outFile,
			includeDetails: true,
		},
	}

	err := action.Run(t.Context())

	require.NoError(t, err)
	content, err := os.ReadFile(outFile)
	require.NoError(t, err)
	assert.Contains(t, string(content), `"insight_count": 0`)
	assert.Contains(t, string(content), `"insights": []`)
}

func TestInsightsExportActionRequiresMonitor(t *testing.T) {
	action := &insightsExportAction{
		client: &fakeInsightsClient{
			monitorPage: &insights_api.MonitorPage{},
		},
		writer: &bytes.Buffer{},
		now:    time.Now,
		flags:  &insightsExportFlags{name: "agent"},
	}

	err := action.Run(t.Context())

	var localErr *azdext.LocalError
	require.ErrorAs(t, err, &localErr)
	assert.Equal(t, "insight_monitor_not_found", localErr.Code)
}

func TestInsightsExportActionRejectsMismatchedMonitor(t *testing.T) {
	action := &insightsExportAction{
		client: &fakeInsightsClient{
			monitorPage: &insights_api.MonitorPage{
				Data: []insights_api.Monitor{{ID: "monitor-other", AgentName: "other-agent"}},
			},
		},
		writer: &bytes.Buffer{},
		now:    time.Now,
		flags:  &insightsExportFlags{name: "requested-agent"},
	}

	err := action.Run(t.Context())

	var localErr *azdext.LocalError
	require.ErrorAs(t, err, &localErr)
	assert.Equal(t, "insight_monitor_not_found", localErr.Code)
}

func TestInsightsExportActionRejectsAmbiguousMonitor(t *testing.T) {
	action := &insightsExportAction{
		client: &fakeInsightsClient{
			monitorPage: &insights_api.MonitorPage{
				Data: []insights_api.Monitor{
					{ID: "monitor-1", AgentName: "agent"},
					{ID: "monitor-2", AgentName: "agent"},
				},
			},
		},
		writer: &bytes.Buffer{},
		now:    time.Now,
		flags:  &insightsExportFlags{name: "agent"},
	}

	err := action.Run(t.Context())

	var localErr *azdext.LocalError
	require.ErrorAs(t, err, &localErr)
	assert.Equal(t, "insight_monitor_ambiguous", localErr.Code)
}

func TestFetchAllInsightsRejectsMissingContinuationCursor(t *testing.T) {
	client := &fakeInsightsClient{
		pages: map[string]*insights_api.InsightPage{
			"": {HasMore: true},
		},
	}

	_, err := fetchAllInsights(t.Context(), client, "monitor-1", &insightsExportFlags{})

	require.ErrorContains(t, err, "without a continuation cursor")
}

func TestFetchAllInsightsRejectsRepeatedContinuationCursor(t *testing.T) {
	client := &fakeInsightsClient{
		pages: map[string]*insights_api.InsightPage{
			"": {
				LastID:  "insight-1",
				HasMore: true,
			},
			"insight-1": {
				LastID:  "insight-1",
				HasMore: true,
			},
		},
	}

	_, err := fetchAllInsights(t.Context(), client, "monitor-1", &insightsExportFlags{})

	require.ErrorContains(t, err, "repeated continuation cursor")
}
