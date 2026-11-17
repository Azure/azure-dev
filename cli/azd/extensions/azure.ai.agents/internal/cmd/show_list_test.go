// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"azureaiagent/internal/exterrors"
	"azureaiagent/internal/pkg/agents/agent_api"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type fakeAgentInventory struct {
	agentPages   []*agent_api.AgentList
	versionPages []*agent_api.AgentVersionList
	err          error
	afters       []string
	gotName      string
	gotVersion   string
}

func (f *fakeAgentInventory) record(after *string) int {
	value := ""
	if after != nil {
		value = *after
	}
	f.afters = append(f.afters, value)
	return len(f.afters) - 1
}

func (f *fakeAgentInventory) ListAgents(
	_ context.Context, params *agent_api.ListAgentQueryParameters, _ string,
) (*agent_api.AgentList, error) {
	i := f.record(params.After)
	if f.err != nil {
		return nil, f.err
	}
	return f.agentPages[i], nil
}

func (f *fakeAgentInventory) ListAgentVersions(
	_ context.Context, name string, params *agent_api.CommonPageQueryParameters, apiVersion string,
) (*agent_api.AgentVersionList, error) {
	f.gotName, f.gotVersion = name, apiVersion
	i := f.record(params.After)
	if f.err != nil {
		return nil, f.err
	}
	return f.versionPages[i], nil
}

func agentPage(hasMore bool, lastID string, agents ...*agent_api.AgentObject) *agent_api.AgentList {
	page := &agent_api.AgentList{}
	for _, agent := range agents {
		page.Data = append(page.Data, *agent)
	}
	page.HasMore, page.LastID = hasMore, lastID
	return page
}

func TestRunListAgents_FollowsEveryPage(t *testing.T) {
	support := agentWithKind("support", "hosted")
	support.ID = "support-id"
	support.Versions.Latest.CreatedAt = 1700000000
	client := &fakeAgentInventory{agentPages: []*agent_api.AgentList{
		agentPage(true, "support-id", support),
		agentPage(false, "", agentWithKind("faq", "prompt")),
	}}
	var out, errOut bytes.Buffer

	err := runListAgents(t.Context(), client, testProjectEndpoint, SourceFlag, "json", &out, &errOut)

	require.NoError(t, err)
	assert.Equal(t, []string{"", "support-id"}, client.afters)
	assert.Empty(t, errOut.String())
	var got agentListResult
	require.NoError(t, json.Unmarshal(out.Bytes(), &got))
	assert.Equal(t, testProjectEndpoint, got.ProjectEndpoint)
	assert.Equal(t, SourceFlag, got.EndpointSource)
	assert.Equal(t, []agentListSummary{
		{Name: "support", ID: "support-id", Kind: "hosted", LatestVersion: "3", Status: "active",
			CreatedAt: "2023-11-14T22:13:20Z"},
		{Name: "faq", Kind: "prompt", LatestVersion: "3", Status: "active"},
	}, got.Data)
}

func TestRunListAgents_RejectsStalledPagination(t *testing.T) {
	for name, pages := range map[string][]*agent_api.AgentList{
		"missing cursor":  {agentPage(true, "")},
		"repeated cursor": {agentPage(true, "a"), agentPage(true, "a")},
	} {
		t.Run(name, func(t *testing.T) {
			client := &fakeAgentInventory{agentPages: pages}
			var out bytes.Buffer
			err := runListAgents(t.Context(), client, testProjectEndpoint, SourceFlag, "json", &out, &out)
			requireLocalErrorCode(t, err, exterrors.CodeInvalidServiceResponse)
			assert.Empty(t, out.String(), "partial results must not be printed")
		})
	}
}

func TestRunListAgents_EmptyState(t *testing.T) {
	t.Run("table", func(t *testing.T) {
		client := &fakeAgentInventory{agentPages: []*agent_api.AgentList{agentPage(false, "")}}
		var out, errOut bytes.Buffer
		require.NoError(t, runListAgents(t.Context(), client, testProjectEndpoint, SourceGlobalConfig,
			"table", &out, &errOut))
		assert.Equal(t, "Project: "+testProjectEndpoint+" (from azd ai project set)\n\nNo agents found.\n",
			out.String())
		assert.Empty(t, errOut.String())
	})
	t.Run("json keeps stdout machine-readable", func(t *testing.T) {
		client := &fakeAgentInventory{agentPages: []*agent_api.AgentList{agentPage(false, "")}}
		var out, errOut bytes.Buffer
		require.NoError(t, runListAgents(t.Context(), client, testProjectEndpoint, SourceFlag,
			"json", &out, &errOut))
		var got map[string]any
		require.NoError(t, json.Unmarshal(out.Bytes(), &got))
		assert.Equal(t, []any{}, got["data"])
		assert.Equal(t, "No agents found in project "+testProjectEndpoint+".\n", errOut.String())
	})
}

func TestRunListAgents_TablePrintsAgents(t *testing.T) {
	client := &fakeAgentInventory{agentPages: []*agent_api.AgentList{
		agentPage(false, "", agentWithKind("support", "hosted")),
	}}
	var out bytes.Buffer
	require.NoError(t, runListAgents(t.Context(), client, testProjectEndpoint, SourceFlag, "table", &out, &out))
	assert.Contains(t, out.String(), "NAME     KIND    LATEST VERSION  STATUS  CREATED")
	assert.Contains(t, out.String(), "support  hosted  3               active")
}

func TestRunListAgents_ServiceErrors(t *testing.T) {
	t.Run("missing project", func(t *testing.T) {
		client := &fakeAgentInventory{err: newTestResponseError(t, http.StatusNotFound)}
		err := runListAgents(t.Context(), client, testProjectEndpoint, SourceFlag, "json", nil, nil)
		requireLocalErrorCode(t, err, exterrors.CodeProjectNotFound)
	})
	t.Run("access denied", func(t *testing.T) {
		client := &fakeAgentInventory{err: newTestResponseError(t, http.StatusForbidden)}
		err := runListAgents(t.Context(), client, testProjectEndpoint, SourceFlag, "json", nil, nil)
		serviceErr, ok := errors.AsType[*azdext.ServiceError](err)
		require.True(t, ok, "expected ServiceError, got %T: %v", err, err)
		assert.Contains(t, serviceErr.ErrorCode, exterrors.OpListAgents)
		assert.Contains(t, serviceErr.Suggestion, "Azure AI User")
	})
	t.Run("resolved endpoint names its source", func(t *testing.T) {
		client := &fakeAgentInventory{err: newTestResponseError(t, http.StatusInternalServerError)}
		err := runListAgents(t.Context(), client, testProjectEndpoint, SourceGlobalConfig, "json", nil, nil)
		serviceErr, ok := errors.AsType[*azdext.ServiceError](err)
		require.True(t, ok, "expected ServiceError, got %T: %v", err, err)
		assert.Contains(t, serviceErr.Suggestion, "azd ai project set")
		assert.Contains(t, serviceErr.Suggestion, "--project-endpoint")
	})
	t.Run("unreachable resolved endpoint names its source", func(t *testing.T) {
		client := &fakeAgentInventory{err: errors.New("dial tcp: no such host")}
		err := runListAgents(t.Context(), client, testProjectEndpoint, SourceAzdEnv, "json", nil, nil)
		localErr := requireLocalErrorCode(t, err, exterrors.OpListAgents)
		assert.Contains(t, localErr.Suggestion, "azd environment FOUNDRY_PROJECT_ENDPOINT")
	})
}

func TestRunListAgentVersions(t *testing.T) {
	target := &AgentContext{ProjectEndpoint: testProjectEndpoint, Name: "support"}
	description := "first release"
	client := &fakeAgentInventory{versionPages: []*agent_api.AgentVersionList{
		{Data: []agent_api.AgentVersionObject{{Version: "2", ID: "support:2", Status: "active"}},
			CommonListObjectProperties: agent_api.CommonListObjectProperties{HasMore: true, LastID: "support:2"}},
		{Data: []agent_api.AgentVersionObject{{Version: "1", Status: "deleted", Description: &description}}},
	}}
	var out, errOut bytes.Buffer

	require.NoError(t, runListAgentVersions(t.Context(), client, target, "v-test", "json", &out, &errOut))

	assert.Equal(t, "support", client.gotName)
	assert.Equal(t, "v-test", client.gotVersion)
	assert.Equal(t, []string{"", "support:2"}, client.afters)
	var got agentVersionListResult
	require.NoError(t, json.Unmarshal(out.Bytes(), &got))
	assert.Equal(t, agentVersionListResult{
		ProjectEndpoint: testProjectEndpoint,
		AgentName:       "support",
		Data: []agentVersionSummary{
			{Version: "2", ID: "support:2", Status: "active"},
			{Version: "1", Status: "deleted", Description: "first release"},
		},
	}, got)
}

func TestRunListAgentVersions_AgentNotFound(t *testing.T) {
	target := &AgentContext{ProjectEndpoint: testProjectEndpoint, Name: "missing"}
	client := &fakeAgentInventory{err: newTestResponseError(t, http.StatusNotFound)}
	err := runListAgentVersions(t.Context(), client, target, DefaultAgentAPIVersion, "table", nil, nil)
	requireLocalErrorCode(t, err, exterrors.CodeAgentNotFound)
}

func projectResult(resp *azdext.GetProjectResponse, err error) projectGetter {
	return func(context.Context, *azdext.EmptyRequest, ...grpc.CallOption) (*azdext.GetProjectResponse, error) {
		return resp, err
	}
}

func stubHostedSources(t *testing.T, sources azdHostedSources) {
	t.Helper()
	original := readAzdHostedSourcesFunc
	t.Cleanup(func() { readAzdHostedSourcesFunc = original })
	readAzdHostedSourcesFunc = func(context.Context) (azdHostedSources, error) { return sources, nil }
}

func TestResolveNamelessShowListScope(t *testing.T) {
	noProject := status.Error(codes.Unknown, "no project exists; to create a new project, run `azd init`")

	t.Run("inside an azd project keeps service resolution", func(t *testing.T) {
		stubHostedSources(t, azdHostedSources{EnvValue: testProjectEndpoint})
		scope, err := resolveNamelessShowListScope(t.Context(),
			projectResult(&azdext.GetProjectResponse{Project: &azdext.ProjectConfig{Name: "app"}}, nil), false)
		require.NoError(t, err)
		assert.Nil(t, scope)
	})
	t.Run("other project errors fall through", func(t *testing.T) {
		scope, err := resolveNamelessShowListScope(t.Context(),
			projectResult(nil, status.Error(codes.Unavailable, "daemon down")), false)
		require.NoError(t, err)
		assert.Nil(t, scope)
	})
	t.Run("outside a project lists the configured project", func(t *testing.T) {
		t.Setenv("FOUNDRY_PROJECT_ENDPOINT", "")
		stubHostedSources(t, azdHostedSources{EnvValue: testProjectEndpoint, EnvName: "dev"})
		scope, err := resolveNamelessShowListScope(t.Context(), projectResult(nil, noProject), false)
		require.NoError(t, err)
		require.NotNil(t, scope)
		assert.Equal(t, testProjectEndpoint, scope.Endpoint)
		assert.Equal(t, SourceAzdEnv, scope.Source)
	})
	t.Run("nil project response lists the configured project", func(t *testing.T) {
		t.Setenv("FOUNDRY_PROJECT_ENDPOINT", testProjectEndpoint)
		stubHostedSources(t, azdHostedSources{})
		scope, err := resolveNamelessShowListScope(t.Context(),
			projectResult(&azdext.GetProjectResponse{}, nil), false)
		require.NoError(t, err)
		require.NotNil(t, scope)
		assert.Equal(t, SourceFoundryEnv, scope.Source)
	})
	t.Run("missing endpoint suggests --project-endpoint", func(t *testing.T) {
		t.Setenv("FOUNDRY_PROJECT_ENDPOINT", "")
		stubHostedSources(t, azdHostedSources{})
		_, err := resolveNamelessShowListScope(t.Context(), projectResult(nil, noProject), false)
		localErr := requireLocalErrorCode(t, err, exterrors.CodeMissingProjectEndpoint)
		assert.Contains(t, localErr.Suggestion, "--project-endpoint")
	})
	t.Run("versions requires a name outside a project", func(t *testing.T) {
		_, err := resolveNamelessShowListScope(t.Context(), projectResult(nil, noProject), true)
		requireLocalErrorCode(t, err, exterrors.CodeInvalidAgentName)
	})
}
