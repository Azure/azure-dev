// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"azureaiagent/internal/pkg/agents/agent_api"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/structpb"
)

func TestStateStoreTargetCanonicalKey(t *testing.T) {
	const want = "account.services.ai.azure.com/api/projects/project/agents/worker"
	for _, endpoint := range []string{
		"https://ACCOUNT.services.ai.azure.com/api/projects/project/agents/worker/endpoint/protocols/invocations",
		"https://account.services.ai.azure.com/api/projects/project/agents/worker/endpoint/" +
			"protocols/openai/responses?api-version=v1",
		"https://account.services.ai.azure.com/api/projects/%70roject/agents/worker/endpoint/protocols/a2a/",
	} {
		target, err := resolveStateStoreTarget(t.Context(), nil, &stateStoreFlags{agentEndpoint: endpoint})
		require.NoError(t, err, "explicit endpoint must work without project or host configuration")
		require.Equal(t, want, target.agentKey)
		require.Equal(t, "worker", target.Name)
		require.Empty(t, target.Version)
	}
	for _, endpoint := range []string{
		"http://account.services.ai.azure.com/api/projects/project/agents/worker/endpoint/protocols/invocations",
		"https://untrusted.example/api/projects/project/agents/worker/endpoint/protocols/invocations",
		"https://account.services.ai.azure.com/api/projects/project",
	} {
		_, err := stateStoreTargetFromEndpoint(endpoint)
		require.Error(t, err)
	}
	_, err := stateStoreTargetFromEndpoint(
		"wss://account.services.ai.azure.com/api/projects/project/agents/worker/endpoint/protocols/invocations_ws")
	require.ErrorContains(t, err, "does not accept WebSocket (wss)")
	local, ok := errors.AsType[*azdext.LocalError](err)
	require.True(t, ok)
	require.Contains(t, local.Suggestion, "--agent-name")
}

func TestStateStoreTargetUsesDeployedAgentAndProject(t *testing.T) {
	const endpoint = "https://account.services.ai.azure.com/api/projects/project"
	for _, version := range []string{"1", "2"} {
		for _, projectOutput := range []bool{true, false} {
			values := map[string]string{
				"AGENT_SERVICE_KEY_NAME": "deployed-agent", "AGENT_SERVICE_KEY_VERSION": version,
				"AGENT_SERVICE_KEY_ENDPOINT": endpoint + "/agents/deployed-agent/versions/" + version,
			}
			if projectOutput {
				values["AGENT_SERVICE_KEY_PROJECT_ENDPOINT"] = endpoint
			}
			project := &helpersProjectServer{project: &azdext.ProjectConfig{
				Path: t.TempDir(), Services: map[string]*azdext.ServiceConfig{
					"service-key": {Name: "service-key", Host: AiAgentHost},
				},
			}}
			env := &testEnvironmentServiceServer{
				current: &azdext.Environment{Name: "test"}, values: map[string]map[string]string{"test": values},
			}
			host := newHelpersTestAzdClient(t, project, &helpersPromptServer{}, env)
			target, err := resolveStateStoreTarget(t.Context(), host,
				&stateStoreFlags{agentName: "service-key", noPrompt: true})
			require.NoError(t, err)
			require.Equal(t, "deployed-agent", target.Name, "service selector is not the deployed agent name")
			require.Equal(t, endpoint, target.ProjectEndpoint)
			require.Equal(t, "account.services.ai.azure.com/api/projects/project/agents/deployed-agent", target.agentKey)
			require.Empty(t, target.Version)
		}
	}
}

func TestStateStoreExplicitEnvironment(t *testing.T) {
	const endpoint = "https://account.services.ai.azure.com/api/projects/alternate"
	for _, source := range []string{"service", "resource", "environment", "missing"} {
		t.Run(source, func(t *testing.T) {
			values := map[string]string{"AGENT_WORKER_NAME": "alternate-agent", "AGENT_WORKER_VERSION": "3"}
			switch source {
			case "service":
				values["AGENT_WORKER_PROJECT_ENDPOINT"] = endpoint
			case "resource":
				values["AGENT_WORKER_ENDPOINT"] = endpoint + "/agents/alternate-agent/versions/3"
			case "environment":
				values["FOUNDRY_PROJECT_ENDPOINT"] = endpoint
			}
			project := &helpersProjectServer{project: &azdext.ProjectConfig{
				Path: t.TempDir(), Services: map[string]*azdext.ServiceConfig{
					"worker": {Name: "worker", Host: AiAgentHost},
				},
			}}
			env := &testEnvironmentServiceServer{
				current:      &azdext.Environment{Name: "default"},
				environments: map[string]*azdext.Environment{"alternate": {Name: "alternate"}},
				values: map[string]map[string]string{
					"alternate": values,
					"default":   {"AGENT_WORKER_NAME": "wrong-agent", "FOUNDRY_PROJECT_ENDPOINT": endpoint + "-wrong"},
				},
			}
			host := newHelpersTestAzdClient(t, project, &helpersPromptServer{}, env)
			flags := &stateStoreFlags{agentName: "worker", environment: "alternate", noPrompt: true}
			target, err := resolveStateStoreTarget(t.Context(), host, flags)
			if source == "missing" {
				require.ErrorContains(t, err, "no Foundry project endpoint")
			} else {
				require.NoError(t, err)
				require.Equal(t, "alternate-agent", target.Name)
				require.Equal(t, endpoint, target.ProjectEndpoint)
			}
			require.Zero(t, env.getCurrentCalls, "explicit selection must never read default environment metadata")
			flags.environment = "nonexistent"
			_, err = resolveStateStoreTarget(t.Context(), host, flags)
			require.ErrorContains(t, err, "nonexistent")
		})
	}
}

func TestStateStoreDefaultEnvironmentDoesNotUseUnrelatedEndpoint(t *testing.T) {
	const endpoint = "https://account.services.ai.azure.com/api/projects/current"
	t.Setenv("FOUNDRY_PROJECT_ENDPOINT", "https://other.services.ai.azure.com/api/projects/wrong")
	for _, source := range []string{"service", "resource", "environment", "missing"} {
		t.Run(source, func(t *testing.T) {
			values := map[string]string{"AGENT_WORKER_NAME": "worker"}
			switch source {
			case "service":
				values["AGENT_WORKER_PROJECT_ENDPOINT"] = endpoint
			case "resource":
				values["AGENT_WORKER_ENDPOINT"] = endpoint + "/agents/worker/versions/1"
			case "environment":
				values["FOUNDRY_PROJECT_ENDPOINT"] = endpoint
			}
			project := &helpersProjectServer{project: &azdext.ProjectConfig{
				Path: t.TempDir(), Services: map[string]*azdext.ServiceConfig{
					"worker": {Name: "worker", Host: AiAgentHost},
				},
			}}
			env := &testEnvironmentServiceServer{
				current: &azdext.Environment{Name: "dev"}, values: map[string]map[string]string{"dev": values},
			}
			host := newHelpersTestAzdClient(t, project, &helpersPromptServer{}, env)
			target, err := resolveStateStoreTarget(t.Context(), host,
				&stateStoreFlags{agentName: "worker", noPrompt: true})
			if source == "missing" {
				require.ErrorContains(t, err, `no Foundry project endpoint in environment "dev"`)
				require.Nil(t, target)
			} else {
				require.NoError(t, err)
				require.Equal(t, endpoint, target.ProjectEndpoint)
			}
			require.Equal(t, 1, env.getCurrentCalls, "endpoint must come from the name's environment")
		})
	}
}

func TestStateStoreRequiresHostedService(t *testing.T) {
	const endpoint = "https://account.services.ai.azure.com/api/projects/project"
	for _, kind := range []string{"hosted", "prompt", "voice", "prompt-voice"} {
		t.Run(kind, func(t *testing.T) {
			properties, err := structpb.NewStruct(map[string]any{"kind": kind})
			require.NoError(t, err)
			project := &helpersProjectServer{project: &azdext.ProjectConfig{
				Path: t.TempDir(), Services: map[string]*azdext.ServiceConfig{
					"agent": {Name: "agent", Host: AiAgentHost, AdditionalProperties: properties},
				},
			}}
			env := &testEnvironmentServiceServer{
				current: &azdext.Environment{Name: "dev"}, values: map[string]map[string]string{"dev": {
					"AGENT_AGENT_NAME": "deployed-agent", "AGENT_AGENT_PROJECT_ENDPOINT": endpoint,
				}},
			}
			host := newHelpersTestAzdClient(t, project, &helpersPromptServer{}, env)
			target, err := resolveStateStoreTarget(t.Context(), host,
				&stateStoreFlags{agentName: "agent", noPrompt: true})
			if kind == "hosted" {
				require.NoError(t, err)
				require.Equal(t, "deployed-agent", target.Name)
			} else {
				require.ErrorContains(t, err, "State Stores require a hosted agent")
				require.Nil(t, target)
				require.Zero(t, env.getCurrentCalls, "reject non-hosted services before reading deployment metadata")
			}
		})
	}
}

func TestStateStoreTargetMultipleServices(t *testing.T) {
	project := &helpersProjectServer{project: &azdext.ProjectConfig{
		Path: t.TempDir(), Services: map[string]*azdext.ServiceConfig{
			"first":  {Name: "first", Host: AiAgentHost},
			"second": {Name: "second", Host: AiAgentHost},
		},
	}}
	const endpoint = "https://account.services.ai.azure.com/api/projects/second"
	env := &testEnvironmentServiceServer{
		current: &azdext.Environment{Name: "test"}, values: map[string]map[string]string{"test": {
			"AGENT_FIRST_NAME":             "other-agent",
			"AGENT_FIRST_PROJECT_ENDPOINT": "https://account.services.ai.azure.com/api/projects/first",
			"AGENT_SECOND_NAME":            "picked-agent", "AGENT_SECOND_PROJECT_ENDPOINT": endpoint,
		}},
	}
	prompt := &helpersPromptServer{selectIndex: 1}
	host := newHelpersTestAzdClient(t, project, prompt, env)
	_, err := resolveStateStoreTarget(t.Context(), host, &stateStoreFlags{noPrompt: true})
	require.Error(t, err)
	require.NotContains(t, err.Error(), "positional")
	local, ok := errors.AsType[*azdext.LocalError](err)
	require.True(t, ok)
	require.Contains(t, local.Suggestion, "--agent-name")
	require.EqualValues(t, 0, prompt.selectCalls.Load())
	target, err := resolveStateStoreTarget(t.Context(), host, &stateStoreFlags{})
	require.NoError(t, err)
	require.Equal(t, "picked-agent", target.Name)
	require.Equal(t, endpoint, target.ProjectEndpoint)
}

func TestStateStoreSelectionCancellation(t *testing.T) {
	a, api, _, writer := newStateStoreTestAction(t)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	requireStateStoreCancelled(t, a.run(ctx, "show", nil))
	api.On("GetStateStore", mock.Anything, "worker", "store").Return(&agent_api.StateStore{Name: "store"}, nil)
	requireStateStoreCancelled(t, a.run(ctx, "select", []string{"store"}))
	require.Empty(t, writer.String())
}

func TestStateStoreRejectsOversizedSavedSelection(t *testing.T) {
	a, _, _, _ := newStateStoreTestAction(t)
	saveAgentStateForTest(t, a.host, a.target.agentKey, agentStateRecord{StateStore: strings.Repeat("s", 129)})
	require.ErrorContains(t, a.run(t.Context(), "items show", []string{"key"}),
		"store name exceeds 128 characters")
	require.ErrorContains(t, a.run(t.Context(), "select", []string{strings.Repeat("s", 129)}),
		"store name exceeds 128 characters")
}

func TestStateStoreSelectionPersistence(t *testing.T) {
	a, api, _, writer := newStateStoreTestAction(t)
	saveAgentStateForTest(t, a.host, a.target.agentKey, agentStateRecord{StateStore: "old"})
	saveAgentStateForTest(t, a.host, "another-agent", agentStateRecord{StateStore: "unrelated"})
	saveAgentStateForTest(t, a.host, "session-key", agentStateRecord{SessionID: "session-id"})
	saveAgentStateForTest(t, a.host, "conversation-key", agentStateRecord{ConversationID: "conversation-id"})
	api.On("GetStateStore", mock.Anything, "worker", "checkpoints/run-42").
		Return(&agent_api.StateStore{Name: "checkpoints/run-42"}, nil).Once()
	require.NoError(t, a.run(t.Context(), "select", []string{"checkpoints/run-42"}))
	require.Contains(t, writer.String(), "checkpoints/run-42")
	selection := loadAgentStateForTest(t, a.host, a.target.agentKey)
	require.NotNil(t, selection)
	require.Equal(t, "checkpoints/run-42", selection.StateStore)
	unrelated := loadAgentStateForTest(t, a.host, "another-agent")
	require.NotNil(t, unrelated)
	require.Equal(t, "unrelated", unrelated.StateStore)
	session := loadAgentStateForTest(t, a.host, "session-key")
	require.NotNil(t, session)
	require.Equal(t, "session-id", session.SessionID)
	conversation := loadAgentStateForTest(t, a.host, "conversation-key")
	require.NotNil(t, conversation)
	require.Equal(t, "conversation-id", conversation.ConversationID)
	store, err := a.resolveStore(t.Context(), "")
	require.NoError(t, err)
	require.Equal(t, "checkpoints/run-42", store)
	// A different project or agent must never inherit this selection.
	a.target.agentKey += "-other"
	_, err = a.resolveStore(t.Context(), "")
	require.ErrorContains(t, err, "no active State Store")
}

func TestStateStoreFailedSelectionRetainsPrevious(t *testing.T) {
	a, api, _, writer := newStateStoreTestAction(t)
	saveAgentStateForTest(t, a.host, a.target.agentKey, agentStateRecord{StateStore: "old"})
	api.On("GetStateStore", mock.Anything, "worker", "new").
		Return(nil, &azcore.ResponseError{StatusCode: 404}).Once()
	require.Error(t, a.run(t.Context(), "select", []string{"new"}))
	require.Empty(t, writer.String())
	selection := loadAgentStateForTest(t, a.host, a.target.agentKey)
	require.NotNil(t, selection)
	require.Equal(t, "old", selection.StateStore)
}

func TestStateStoreMissingAndCorruptSelection(t *testing.T) {
	t.Run("missing", func(t *testing.T) {
		a, _, _, _ := newStateStoreTestAction(t)
		require.ErrorContains(t, a.run(t.Context(), "items show", []string{"key"}), "no active State Store")
	})
	t.Run("corrupt state", func(t *testing.T) {
		a, _, _, _ := newStateStoreTestAction(t)
		store, err := newAgentStateStoreForClient(a.host)
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(store.statePath(a.target.agentKey), []byte("{"), 0o600))
		require.ErrorContains(t, a.run(t.Context(), "show", nil), "could not read")
	})
}

func TestStateStorePagedPicker(t *testing.T) {
	a, api, _, _ := newStateStoreTestAction(t)
	prompt := &stateStorePromptMock{}
	a.prompt = prompt
	first := api.On("ListStateStores", mock.Anything, "worker", a.flags.page).
		Return(&agent_api.StateStorePage[agent_api.StateStore]{
			Data: []agent_api.StateStore{{Name: "first"}}, HasMore: true, LastID: new("cursor"),
		}, nil).Once()
	chooseNext := prompt.On("Select", mock.Anything, mock.MatchedBy(func(req *azdext.SelectRequest) bool {
		return len(req.Options.Choices) == 2 && req.Options.Choices[1].Label == "Next page"
	})).Return(&azdext.SelectResponse{Value: new(int32(1))}, nil).Once()
	nextOptions := a.flags.page
	nextOptions.After = "cursor"
	next := api.On("ListStateStores", mock.Anything, "worker", nextOptions).
		Return(&agent_api.StateStorePage[agent_api.StateStore]{Data: []agent_api.StateStore{{Name: "later"}}}, nil).Once()
	chooseStore := prompt.On("Select", mock.Anything, mock.MatchedBy(func(req *azdext.SelectRequest) bool {
		return len(req.Options.Choices) == 1 && req.Options.Choices[0].Label == "later"
	})).Return(&azdext.SelectResponse{Value: new(int32(0))}, nil).Once()
	validate := api.On("GetStateStore", mock.Anything, "worker", "later").
		Return(&agent_api.StateStore{Name: "later"}, nil).Once()
	mock.InOrder(first, chooseNext, next, chooseStore, validate)
	require.NoError(t, a.run(t.Context(), "select", nil))
	selection := loadAgentStateForTest(t, a.host, a.target.agentKey)
	require.NotNil(t, selection)
	require.Equal(t, "later", selection.StateStore)
	prompt.AssertExpectations(t)
}

func TestStateStorePickerEscapesDisplayOnly(t *testing.T) {
	const rawName = "alpha\n\x1b[31mred\\suffix"
	a, api, _, _ := newStateStoreTestAction(t)
	prompt := &stateStorePromptMock{}
	a.prompt = prompt
	api.On("ListStateStores", mock.Anything, "worker", a.flags.page).
		Return(&agent_api.StateStorePage[agent_api.StateStore]{
			Data: []agent_api.StateStore{{Name: rawName}},
		}, nil).Once()
	prompt.On("Select", mock.Anything, mock.MatchedBy(func(req *azdext.SelectRequest) bool {
		return len(req.Options.Choices) == 1 &&
			req.Options.Choices[0].Label == `alpha\n\x1b[31mred\\suffix` &&
			req.Options.Choices[0].Value == rawName
	})).Return(&azdext.SelectResponse{Value: new(int32(0))}, nil).Once()
	api.On("GetStateStore", mock.Anything, "worker", rawName).
		Return(&agent_api.StateStore{Name: rawName}, nil).Once()
	require.NoError(t, a.run(t.Context(), "select", nil))
	selection := loadAgentStateForTest(t, a.host, a.target.agentKey)
	require.NotNil(t, selection)
	require.Equal(t, rawName, selection.StateStore)
	prompt.AssertExpectations(t)
}

func TestStateStorePickerFailures(t *testing.T) {
	for _, tt := range []struct {
		name      string
		page      *agent_api.StateStorePage[agent_api.StateStore]
		selection *azdext.SelectResponse
		message   string
	}{
		{"empty", &agent_api.StateStorePage[agent_api.StateStore]{}, nil, "no State Stores"},
		{"missing cursor", &agent_api.StateStorePage[agent_api.StateStore]{HasMore: true}, nil, "pagination cursor"},
		{"cancelled", &agent_api.StateStorePage[agent_api.StateStore]{Data: []agent_api.StateStore{{Name: "a"}}},
			&azdext.SelectResponse{}, "cancelled"},
		{"invalid index", &agent_api.StateStorePage[agent_api.StateStore]{Data: []agent_api.StateStore{{Name: "a"}}},
			&azdext.SelectResponse{Value: new(int32(-1))}, "selection index"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			a, api, _, _ := newStateStoreTestAction(t)
			prompt := &stateStorePromptMock{}
			a.prompt = prompt
			api.On("ListStateStores", mock.Anything, "worker", a.flags.page).Return(tt.page, nil).Once()
			if tt.selection != nil {
				prompt.On("Select", mock.Anything, mock.Anything).Return(tt.selection, nil).Once()
			}
			require.ErrorContains(t, a.run(t.Context(), "select", nil), tt.message)
			prompt.AssertExpectations(t)
		})
	}
	t.Run("no prompt", func(t *testing.T) {
		a, _, _, _ := newStateStoreTestAction(t)
		a.flags.noPrompt = true
		require.ErrorContains(t, a.run(t.Context(), "select", nil), "--no-prompt")
	})
}
