// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"azureaiagent/internal/exterrors"
	"azureaiagent/internal/pkg/agents/agent_api"
	"azureaiagent/internal/pkg/agents/agent_yaml"
	"azureaiagent/internal/pkg/recordproxy"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/require"
)

type nonRetriableTransportError struct {
	msg string
}

func (e *nonRetriableTransportError) Error() string { return e.msg }

func (e *nonRetriableTransportError) NonRetriable() {}

func TestPromptAgentRequestHeaders(t *testing.T) {
	settings := &PromptAgentSettings{ModelEndpoint: "https://model.example.com"}
	for _, tt := range []struct {
		name     string
		agent    agent_yaml.PromptAgent
		features string
	}{
		{name: "plain"},
		{name: "Microsoft Copilot", agent: agent_yaml.PromptAgent{
			Harness: agent_yaml.NewPromptHarness(agent_api.ManagedAgentHarnessMicrosoftCopilot),
		}, features: "MicrosoftCopilot=V1Preview"},
		{name: "legacy GitHub Copilot", agent: agent_yaml.PromptAgent{
			Harness: agent_yaml.NewPromptHarness(agent_api.ManagedAgentHarnessGitHubCopilot),
		}, features: "MicrosoftCopilot=V1Preview"},
		{name: "other harness", agent: agent_yaml.PromptAgent{
			Harness: agent_yaml.NewPromptHarness("other"),
		}},
		{name: "Copilot prefix is not a match", agent: agent_yaml.PromptAgent{
			Harness: agent_yaml.NewPromptHarness("microsoft_copilot_preview_other"),
		}},
		{name: "plain skills", agent: agent_yaml.PromptAgent{
			ResolvedSkills: []agent_yaml.HarnessSkillRef{{Name: "skill", Version: "1"}},
		}, features: "Skills=V1Preview"},
		{name: "Microsoft Copilot skills", agent: agent_yaml.PromptAgent{
			Harness:        agent_yaml.NewPromptHarness(agent_api.ManagedAgentHarnessMicrosoftCopilot),
			ResolvedSkills: []agent_yaml.HarnessSkillRef{{Name: "skill", Version: "1"}},
		}, features: "MicrosoftCopilot=V1Preview,Skills=V1Preview"},
		{name: "legacy GitHub Copilot skills", agent: agent_yaml.PromptAgent{
			Harness:        agent_yaml.NewPromptHarness(agent_api.ManagedAgentHarnessGitHubCopilot),
			ResolvedSkills: []agent_yaml.HarnessSkillRef{{Name: "skill", Version: "1"}},
		}, features: "MicrosoftCopilot=V1Preview,Skills=V1Preview"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			headers := promptAgentRequestHeaders(&tt.agent, settings)
			require.Equal(t, "https://model.example.com", headers["x-model-endpoint"])
			if tt.features == "" {
				require.NotContains(t, headers, "Foundry-Features")
			} else {
				require.Equal(t, tt.features, headers["Foundry-Features"])
			}
		})
	}
}

func TestPromptCreateOrUpdatePromptAgent(t *testing.T) {
	const (
		projectEndpoint = "https://acct.services.ai.azure.com/api/projects/proj"
		modelEndpoint   = "https://acct.services.ai.azure.com"
		agentName       = "my-prompt-agent"
	)

	type recordedCall struct {
		method string
		path   string
		query  string
		header http.Header
	}

	managedWithPolicy := agent_yaml.PromptAgent{
		AgentDefinition: agent_yaml.AgentDefinition{Name: agentName, Kind: agent_yaml.AgentKindPrompt},
		Harness:         agent_yaml.NewPromptHarness(agent_api.ManagedAgentHarnessMicrosoftCopilot),
		Policies:        []agent_yaml.Policy{{Type: agent_yaml.PolicyTypeRai, RaiPolicyName: raiPolicyID}},
	}

	tests := []struct {
		name              string
		getCode           int
		getTransportErr   error
		createCode        int
		updateCode        int
		wantCalls         []string
		wantVersion       string
		wantGetServiceErr bool
		wantGetLocalErr   bool
		wantStatusCode    int
		wantErrorCode     string
		wantRaiSuggestion bool
	}{
		{
			name:       "new agent GET 404 then POST /agents 201",
			getCode:    http.StatusNotFound,
			createCode: http.StatusCreated,
			wantCalls: []string{
				"GET /api/projects/proj/agents/my-prompt-agent",
				"POST /api/projects/proj/agents",
			},
			wantVersion: "1",
		},
		{
			name:       "existing agent GET 200 then POST /agents/{name} 200 without 409",
			getCode:    http.StatusOK,
			updateCode: http.StatusOK,
			wantCalls: []string{
				"GET /api/projects/proj/agents/my-prompt-agent",
				"POST /api/projects/proj/agents/my-prompt-agent",
			},
			wantVersion: "2",
		},
		{
			name:       "concurrent creation race GET 404 then POST /agents 409 then POST /agents/{name} 200",
			getCode:    http.StatusNotFound,
			createCode: http.StatusConflict,
			updateCode: http.StatusOK,
			wantCalls: []string{
				"GET /api/projects/proj/agents/my-prompt-agent",
				"POST /api/projects/proj/agents",
				"POST /api/projects/proj/agents/my-prompt-agent",
			},
			wantVersion: "2",
		},
		{
			name:              "preflight GET 403 fails fast without create or update",
			getCode:           http.StatusForbidden,
			wantCalls:         []string{"GET /api/projects/proj/agents/my-prompt-agent"},
			wantGetServiceErr: true,
			wantStatusCode:    http.StatusForbidden,
			wantErrorCode:     "get_agent.get_failed",
		},
		{
			name:              "preflight GET 501 fails fast without create or update",
			getCode:           http.StatusNotImplemented,
			wantCalls:         []string{"GET /api/projects/proj/agents/my-prompt-agent"},
			wantGetServiceErr: true,
			wantStatusCode:    http.StatusNotImplemented,
			wantErrorCode:     "get_agent.get_failed",
		},
		{
			name:            "preflight GET transport error fails fast without create or update",
			getTransportErr: &nonRetriableTransportError{msg: "dial tcp: connection refused"},
			wantCalls:       []string{"GET /api/projects/proj/agents/my-prompt-agent"},
			wantGetLocalErr: true,
			wantErrorCode:   exterrors.OpGetAgent + exterrors.SuffixTransport,
		},
		{
			name:       "create non-409 error returns error without update",
			getCode:    http.StatusNotFound,
			createCode: http.StatusBadRequest,
			wantCalls: []string{
				"GET /api/projects/proj/agents/my-prompt-agent",
				"POST /api/projects/proj/agents",
			},
			wantStatusCode:    http.StatusBadRequest,
			wantErrorCode:     "create_agent.create_failed",
			wantRaiSuggestion: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var calls []recordedCall
			originalTransport := recordproxy.Transport
			t.Cleanup(func() { recordproxy.Transport = originalTransport })

			recordproxy.Transport = hostedResourceErrorTransport(func(req *http.Request) (*http.Response, error) {
				calls = append(calls, recordedCall{
					method: req.Method,
					path:   req.URL.Path,
					query:  req.URL.RawQuery,
					header: req.Header.Clone(),
				})

				var code int
				var body string
				switch {
				case req.Method == http.MethodGet && req.URL.Path == "/api/projects/proj/agents/"+agentName:
					if tt.getTransportErr != nil {
						return nil, tt.getTransportErr
					}
					code = tt.getCode
					if code == http.StatusOK {
						body = `{"name":"` + agentName + `","versions":{"latest":{"version":"1","status":"active"}}}`
					} else {
						body = `{"error":{"code":"get_failed","message":"get error"}}`
					}
				case req.Method == http.MethodPost && req.URL.Path == "/api/projects/proj/agents":
					code = tt.createCode
					if code == http.StatusCreated || code == http.StatusOK {
						body = `{"name":"` + agentName + `","versions":{"latest":{"version":"1","status":"active"}}}`
					} else {
						body = `{"error":{"code":"create_failed","message":"create error"}}`
					}
				case req.Method == http.MethodPost && req.URL.Path == "/api/projects/proj/agents/"+agentName:
					code = tt.updateCode
					if code == http.StatusOK {
						body = `{"name":"` + agentName + `","versions":{"latest":{"version":"2","status":"active"}}}`
					} else {
						body = `{"error":{"code":"update_failed","message":"update error"}}`
					}
				default:
					t.Fatalf("unexpected request: %s %s", req.Method, req.URL.Path)
				}

				return &http.Response{
					StatusCode: code,
					Header:     http.Header{"Content-Type": {"application/json"}},
					Body:       io.NopCloser(strings.NewReader(body)),
					Request:    req,
				}, nil
			})

			settings := &PromptAgentSettings{
				ProjectEndpoint: projectEndpoint,
				ModelEndpoint:   modelEndpoint,
			}
			headers := map[string]string{
				"x-model-endpoint": modelEndpoint,
				"Foundry-Features": agent_api.MicrosoftCopilotPreviewFeature,
			}
			client := agent_api.NewAgentClient(projectEndpoint, hostedResourceErrorCredential{})
			provider := &AgentServiceTargetProvider{}
			req := &agent_api.CreateAgentRequest{
				Name: agentName,
				CreateAgentVersionRequest: agent_api.CreateAgentVersionRequest{
					Description: new("prompt description"),
					Metadata:    map[string]string{"env": "test"},
					Definition: map[string]any{
						"kind":         "prompt",
						"model":        "gpt-4.1-mini",
						"instructions": "Be helpful.",
					},
				},
			}

			got, err := provider.createOrUpdatePromptAgent(t.Context(), client, req, settings, headers)

			gotCallSummary := make([]string, 0, len(calls))
			for _, c := range calls {
				gotCallSummary = append(gotCallSummary, c.method+" "+c.path)
				require.Equal(t, "api-version=v1", c.query)
				require.Equal(t, modelEndpoint, c.header.Get("x-model-endpoint"))
				require.Equal(t, agent_api.MicrosoftCopilotPreviewFeature, c.header.Get("Foundry-Features"))
			}
			require.Equal(t, tt.wantCalls, gotCallSummary)

			switch {
			case tt.wantGetServiceErr:
				require.Error(t, err)
				require.Nil(t, got)
				serviceErr, ok := errors.AsType[*azdext.ServiceError](err)
				require.True(t, ok, "expected *azdext.ServiceError, got %T: %v", err, err)
				require.Equal(t, tt.wantErrorCode, serviceErr.ErrorCode)
				require.Equal(t, tt.wantStatusCode, serviceErr.StatusCode)

				deployErr := promptCreateError(err, &managedWithPolicy, "my-prompt-svc")
				deployServiceErr, ok := errors.AsType[*azdext.ServiceError](deployErr)
				require.True(t, ok)
				require.Same(t, serviceErr, deployServiceErr)
				require.Equal(t, tt.wantErrorCode, deployServiceErr.ErrorCode)
				require.NotContains(t, deployServiceErr.Suggestion, "Responsible AI policy")
			case tt.wantGetLocalErr:
				require.Error(t, err)
				require.Nil(t, got)
				localErr, ok := errors.AsType[*azdext.LocalError](err)
				require.True(t, ok, "expected *azdext.LocalError, got %T: %v", err, err)
				require.Equal(t, tt.wantErrorCode, localErr.Code)
				require.Equal(t, azdext.LocalErrorCategoryDependency, localErr.Category)
				require.NotEmpty(t, localErr.Suggestion)

				deployErr := promptCreateError(err, &managedWithPolicy, "my-prompt-svc")
				deployLocalErr, ok := errors.AsType[*azdext.LocalError](deployErr)
				require.True(t, ok)
				require.Same(t, localErr, deployLocalErr)
				require.Equal(t, tt.wantErrorCode, deployLocalErr.Code)
				require.NotContains(t, deployLocalErr.Suggestion, "Responsible AI policy")
			case tt.wantStatusCode != 0:
				require.Error(t, err)
				require.Nil(t, got)
				respErr, ok := errors.AsType[*azcore.ResponseError](err)
				require.True(t, ok, "expected *azcore.ResponseError, got %T: %v", err, err)
				require.Equal(t, tt.wantStatusCode, respErr.StatusCode)

				deployErr := promptCreateError(err, &managedWithPolicy, "my-prompt-svc")
				deployServiceErr, ok := errors.AsType[*azdext.ServiceError](deployErr)
				require.True(t, ok)
				require.Equal(t, tt.wantErrorCode, deployServiceErr.ErrorCode)
				require.Equal(t, tt.wantStatusCode, deployServiceErr.StatusCode)
				if tt.wantRaiSuggestion {
					require.Contains(t, deployServiceErr.Suggestion, "Responsible AI policy")
				} else {
					require.NotContains(t, deployServiceErr.Suggestion, "Responsible AI policy")
				}
			default:
				require.NoError(t, err)
				require.NotNil(t, got)
				require.Equal(t, agentName, got.Name)
				require.Equal(t, tt.wantVersion, got.Versions.Latest.Version)
			}
		})
	}
}
