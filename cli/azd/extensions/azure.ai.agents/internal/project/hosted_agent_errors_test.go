// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"azureaiagent/internal/exterrors"
	"azureaiagent/internal/pkg/agents/agent_api"
	"azureaiagent/internal/pkg/agents/agent_yaml"
	"azureaiagent/internal/pkg/recordproxy"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/output/ux"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/structpb"
)

const resourceTierErrorMessage = "CPU and Memory must be specified as a valid resource tier. " +
	"Valid tiers: (0.5, 1Gi), (1, 2Gi), (2, 4Gi), (4, 8Gi)"

func hostedResourceRequest(cpu, memory string) *agent_api.CreateAgentRequest {
	return &agent_api.CreateAgentRequest{
		Name: "foundry-agent",
		CreateAgentVersionRequest: agent_api.CreateAgentVersionRequest{
			Definition: agent_api.HostedAgentDefinition{CPU: cpu, Memory: memory},
		},
	}
}

func resourceValidationBody(t *testing.T, message, param string) string {
	t.Helper()
	body, err := json.Marshal(map[string]any{
		"error": map[string]any{
			"code": "invalid_parameters", "type": "invalid_request_error",
			"message": message, "param": param,
			"details": []any{map[string]any{
				"code": "ValidationError", "message": message, "param": param,
			}},
		},
	})
	require.NoError(t, err)
	return string(body)
}

func resourceHTTPError(t *testing.T, statusCode int, body string) error {
	t.Helper()
	requestURL, err := url.Parse("https://example.test/agents/foundry-agent/versions")
	require.NoError(t, err)
	return runtime.NewResponseError(&http.Response{
		StatusCode: statusCode,
		Header:     http.Header{"Content-Type": {"application/json"}, "X-Request-Id": {"resource-request-id"}},
		Body:       io.NopCloser(strings.NewReader(body)),
		Request:    &http.Request{Method: http.MethodPost, URL: requestURL},
	})
}

func TestHostedAgentCreateErrorResourceValidation(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name, cpu, memory, message, param string
	}{
		{"mismatched pair", "4", "4Gi", resourceTierErrorMessage, "definition"},
		{"unsupported tier", "8", "16Gi", resourceTierErrorMessage, "definition"},
		{"defaulted memory", "4", DefaultMemory, resourceTierErrorMessage, "definition"},
		{"CPU format", "4core", "8Gi", "CPU must be a decimal number representing cores.", "definition.cpu"},
		{"memory format", "4", "8GB", "Memory must be specified in GiB format only.", "definition.memory"},
		{"field-specific message", "4", "8Gi", "Requested allocation is unavailable.", "/definition/cpu"},
		{"reason without parameter", "4", "4Gi", resourceTierErrorMessage, ""},
		{"future tier guidance", "32", "64Gi",
			"CPU and Memory must be specified as a valid resource tier. Valid tiers: (16, 32Gi)", "definition"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			original := resourceHTTPError(t, http.StatusBadRequest, resourceValidationBody(t, tt.message, tt.param))
			result := hostedAgentCreateError(original, "my-service", hostedResourceRequest(tt.cpu, tt.memory))
			serviceErr, ok := errors.AsType[*azdext.ServiceError](result)
			require.True(t, ok)
			require.Equal(t, "create_agent.invalid_parameters", serviceErr.ErrorCode)
			require.Equal(t, http.StatusBadRequest, serviceErr.StatusCode)
			require.Equal(t, "example.test", serviceErr.ServiceName)
			require.Contains(t, serviceErr.Message, `service "my-service"`)
			require.Contains(t, serviceErr.Message, `CPU="`+tt.cpu+`"`)
			require.Contains(t, serviceErr.Message, `memory="`+tt.memory+`"`)
			require.Contains(t, serviceErr.Message, tt.message)
			require.Contains(t, serviceErr.Message, "HTTP 400")
			require.Contains(t, serviceErr.Message, "resource-request-id")
			require.Contains(t, serviceErr.Suggestion, "container.resources.cpu")
			require.Contains(t, serviceErr.Suggestion, "container.resources.memory")
			require.Contains(t, serviceErr.Suggestion, "$ref")
			require.Contains(t, serviceErr.Suggestion, `azd deploy "my-service"`)

			relayed := azdext.UnwrapError(azdext.WrapError(result))
			require.Equal(t, serviceErr.Message, azdext.ErrorMessage(relayed))
			require.Equal(t, serviceErr.Suggestion, azdext.ErrorSuggestion(relayed))
			relayedService, ok := errors.AsType[*azdext.ServiceError](relayed)
			require.True(t, ok)
			require.Equal(t, serviceErr.ErrorCode, relayedService.ErrorCode)
			require.Equal(t, serviceErr.StatusCode, relayedService.StatusCode)
			require.Equal(t, serviceErr.ServiceName, relayedService.ServiceName)
			display := (&ux.ErrorWithSuggestion{
				Message: azdext.ErrorMessage(relayed), Suggestion: azdext.ErrorSuggestion(relayed),
			}).ToString("")
			require.Contains(t, display, `Foundry rejected the resource configuration for service "my-service"`)
			require.Contains(t, display, tt.message)
			require.Contains(t, display, "Suggestion:")
			require.Contains(t, display, `azd deploy "my-service"`)
		})
	}
}

func TestHostedAgentCreateErrorLeavesOtherErrorsUnchanged(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name, body string
		status     int
	}{
		{"unrelated validation", resourceValidationBody(t, "Agent name is invalid.", "name"), http.StatusBadRequest},
		{"mentions CPU in instructions",
			resourceValidationBody(t, "Instructions mention CPU must be decimal.", "instructions"),
			http.StatusBadRequest},
		{"resource-looking instructions", resourceValidationBody(t, "CPU must be a decimal number.", "instructions"),
			http.StatusBadRequest},
		{"resource-looking quota error", resourceValidationBody(t, "CPU must fit within the quota.", "quota"),
			http.StatusBadRequest},
		{"unrecognized definition reason", resourceValidationBody(t, "The requested definition is invalid.", "definition"),
			http.StatusBadRequest},
		{"authentication", resourceValidationBody(t, resourceTierErrorMessage, "definition"), http.StatusUnauthorized},
		{"service failure", resourceValidationBody(t, resourceTierErrorMessage, "definition"),
			http.StatusInternalServerError},
		{"malformed response", "not-json", http.StatusBadRequest},
		{"unexpected response shape", `{"error":"invalid parameters"}`, http.StatusBadRequest},
		{"empty response", "", http.StatusBadRequest},
		{"mixed validation", `{"error":{"code":"invalid_parameters","message":"Invalid parameters","details":[
			{"message":"CPU must be a decimal number.","param":"definition.cpu"},
			{"message":"Agent name is invalid.","param":"name"}]}}`, http.StatusBadRequest},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			original := resourceHTTPError(t, tt.status, tt.body)
			expected := exterrors.ServiceFromAzure(original, exterrors.OpCreateAgent)
			result := hostedAgentCreateError(original, "my-service", hostedResourceRequest("4", "4Gi"))
			require.Equal(t, expected, result)
		})
	}

	original := exterrors.Validation(exterrors.CodeInvalidParameter, "invalid input", "fix the input")
	require.Same(t, original, hostedAgentCreateError(original, "my-service", hostedResourceRequest("4", "4Gi")))
}

func TestHostedAgentCreateErrorTopLevelAndMultipleResourceMessages(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name, body string
		messages   []string
	}{
		{"top-level only",
			`{"error":{"code":"invalid_parameters","message":"CPU must be a decimal number.","param":"definition.cpu"}}`,
			[]string{"CPU must be a decimal number."}},
		{"multiple fields", `{"error":{"code":"invalid_parameters","message":"Invalid parameters","details":[
			{"message":"CPU must be a decimal number.","param":"definition.cpu"},
			{"message":"Memory must be specified in GiB format only.","param":"definition.memory"}]}}`,
			[]string{"CPU must be a decimal number.", "Memory must be specified in GiB format only."}},
		{"duplicate messages", `{"error":{"code":"invalid_parameters","details":[
			{"message":"CPU must be a decimal number.","param":"definition.cpu"},
			{"message":"CPU must be a decimal number.","param":"definition.cpu"}]}}`,
			[]string{"CPU must be a decimal number."}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := hostedAgentCreateError(
				resourceHTTPError(t, http.StatusBadRequest, tt.body), "my-service", hostedResourceRequest("4", "4Gi"),
			)
			for _, message := range tt.messages {
				require.Contains(t, azdext.ErrorMessage(result), message)
				require.Equal(t, 1, strings.Count(azdext.ErrorMessage(result), message))
			}
			require.NotEmpty(t, azdext.ErrorSuggestion(result))
		})
	}
}

func TestHostedAgentCreateErrorRetryCommandSafety(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name       string
		service    string
		safeToCopy bool
	}{
		{"ordinary service", "my-service", true},
		{"space in service", "my service", true},
		{"command substitution", "$(Invoke-Expression evil)", false},
		{"backtick substitution", "`evil`", false},
		{"environment expansion", "%PATH%", false},
		{"double quote", `my"service`, false},
		{"single quote", "my'service", false},
		{"leading option", "--all", false},
		{"line break", "my\nservice", false},
		{"empty name", "", false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			original := resourceHTTPError(
				t, http.StatusBadRequest, resourceValidationBody(t, resourceTierErrorMessage, "definition"),
			)
			result := hostedAgentCreateError(original, tt.service, hostedResourceRequest("4", "4Gi"))
			suggestion := azdext.ErrorSuggestion(azdext.UnwrapError(azdext.WrapError(result)))
			if tt.safeToCopy {
				require.Contains(t, suggestion, `retry 'azd deploy "`+tt.service+`"'.`)
			} else {
				require.NotContains(t, suggestion, "retry 'azd deploy")
				require.Contains(t, suggestion, "shell-escaped argument to azd deploy")
			}
		})
	}
}

func TestHostedAgentCreateErrorDoesNotDiscloseRequestURLCredentials(t *testing.T) {
	t.Parallel()
	original := resourceHTTPError(
		t, http.StatusBadRequest, resourceValidationBody(t, resourceTierErrorMessage, "definition"),
	)
	responseErr, ok := errors.AsType[*azcore.ResponseError](original)
	require.True(t, ok)
	requestURL, err := url.Parse(
		"https://username:secret-password@example.test/?sig=secret-sas#secret-fragment",
	)
	require.NoError(t, err)
	responseErr.RawResponse.Request.URL = requestURL
	result := hostedAgentCreateError(original, "my-service", hostedResourceRequest("4", "4Gi"))
	relayed := azdext.UnwrapError(azdext.WrapError(result))
	output := azdext.ErrorMessage(relayed) + azdext.ErrorSuggestion(relayed)
	for _, secret := range []string{"username", "secret-password", "secret-sas", "secret-fragment"} {
		require.NotContains(t, output, secret)
	}
}

func TestHostedAgentCreateErrorPreservesNonHostedAndMissingRequests(t *testing.T) {
	t.Parallel()
	original := resourceHTTPError(
		t, http.StatusBadRequest, resourceValidationBody(t, resourceTierErrorMessage, "definition"),
	)
	expected := exterrors.ServiceFromAzure(original, exterrors.OpCreateAgent)
	for _, request := range []*agent_api.CreateAgentRequest{
		nil,
		{CreateAgentVersionRequest: agent_api.CreateAgentVersionRequest{
			Definition: agent_api.AgentDefinition{Kind: agent_api.AgentKindPrompt},
		}},
	} {
		require.Equal(t, expected, hostedAgentCreateError(original, "my-service", request))
	}
}

func TestHostedAgentCreateErrorServiceCodeAvailability(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name, code, telemetryCode, serviceResponse string
	}{
		{"code supplied", "invalid_parameters", "create_agent.invalid_parameters",
			"Service response: HTTP 400 (create_agent.invalid_parameters)."},
		{"code omitted", "", "create_agent.400", "Service response: HTTP 400."},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			serviceError := map[string]any{"message": resourceTierErrorMessage, "param": "definition"}
			if tt.code != "" {
				serviceError["code"] = tt.code
			}
			body, err := json.Marshal(map[string]any{"error": serviceError})
			require.NoError(t, err)
			original := resourceHTTPError(t, http.StatusBadRequest, string(body))
			result := hostedAgentCreateError(original, "my-service", hostedResourceRequest("4", "4Gi"))
			relayed := azdext.UnwrapError(azdext.WrapError(result))
			serviceErr, ok := errors.AsType[*azdext.ServiceError](relayed)
			require.True(t, ok)
			require.Equal(t, tt.telemetryCode, serviceErr.ErrorCode)
			require.Contains(t, serviceErr.Message, tt.serviceResponse)
			require.NotContains(t, serviceErr.Message, "create_agent.400")
			require.Contains(t, serviceErr.Message, resourceTierErrorMessage)
			require.NotEmpty(t, serviceErr.Suggestion)
		})
	}
}

func TestHostedAgentCreateErrorRequestIDHeaders(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name      string
		headers   map[string]string
		requestID string
	}{
		{"x-request-id", map[string]string{"x-request-id": "request-id"}, "request-id"},
		{"x-ms-request-id", map[string]string{"x-ms-request-id": "azure-request-id"}, "azure-request-id"},
		{"apim-request-id", map[string]string{"apim-request-id": "apim-id"}, "apim-id"},
		{"prefer x-request-id", map[string]string{
			"x-request-id": "request-id", "x-ms-request-id": "azure-request-id", "apim-request-id": "apim-id",
		}, "request-id"},
		{"prefer x-ms-request-id over APIM", map[string]string{
			"x-ms-request-id": "azure-request-id", "apim-request-id": "apim-id",
		}, "azure-request-id"},
		{"folded x-request-id", map[string]string{
			"x-request-id": " request-id, request-id ",
		}, "request-id"},
		{"folded x-ms-request-id", map[string]string{
			"x-ms-request-id": " azure-request-id, azure-request-id ",
		}, "azure-request-id"},
		{"folded APIM request ID", map[string]string{
			"apim-request-id": " apim-id, apim-id ",
		}, "apim-id"},
		{"first non-empty token", map[string]string{
			"apim-request-id": " , \t, apim-id, other-id ",
		}, "apim-id"},
		{"folded primary keeps precedence", map[string]string{
			"x-request-id": " request-id, duplicate ", "apim-request-id": "apim-id",
		}, "request-id"},
		{"blank request ID", map[string]string{"apim-request-id": " , \t, "}, ""},
		{"no request ID", nil, ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			original := resourceHTTPError(
				t, http.StatusBadRequest, resourceValidationBody(t, resourceTierErrorMessage, "definition"),
			)
			responseErr, ok := errors.AsType[*azcore.ResponseError](original)
			require.True(t, ok)
			responseErr.RawResponse.Header = make(http.Header)
			for header, value := range tt.headers {
				responseErr.RawResponse.Header.Set(header, value)
			}
			result := hostedAgentCreateError(original, "my-service", hostedResourceRequest("4", "4Gi"))
			relayed := azdext.UnwrapError(azdext.WrapError(result))
			if tt.requestID == "" {
				require.NotContains(t, azdext.ErrorMessage(relayed), "Request ID:")
			} else {
				require.Contains(t, strings.Split(azdext.ErrorMessage(relayed), "\n"), "Request ID: "+tt.requestID)
			}
		})
	}
}

type hostedResourceErrorTransport func(*http.Request) (*http.Response, error)

func (f hostedResourceErrorTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

type hostedResourceErrorCredential struct{}

func (hostedResourceErrorCredential) GetToken(context.Context, policy.TokenRequestOptions) (azcore.AccessToken, error) {
	return azcore.AccessToken{Token: "test-token", ExpiresOn: time.Now().Add(time.Hour)}, nil
}

func TestHostedAgentResourceErrorsAcrossDeploymentModes(t *testing.T) {
	t.Setenv("AZURE_LOCATION", "")
	for _, mode := range []string{"container create", "container update", "code create", "code update"} {
		t.Run(mode, func(t *testing.T) {
			body := resourceValidationBody(t, resourceTierErrorMessage, "definition")
			writeRequests := 0
			originalTransport := recordproxy.Transport
			t.Cleanup(func() { recordproxy.Transport = originalTransport })
			recordproxy.Transport = hostedResourceErrorTransport(func(request *http.Request) (*http.Response, error) {
				statusCode := http.StatusBadRequest
				responseBody := body
				if request.Method == http.MethodGet {
					statusCode = http.StatusNotFound
					responseBody = `{"error":{"code":"not_found"}}`
					if strings.HasSuffix(mode, "update") {
						statusCode = http.StatusOK
						responseBody = `{"name":"foundry-agent","versions":{"latest":{"version":"1"}}}`
					}
				} else {
					writeRequests++
				}
				return &http.Response{
					StatusCode: statusCode,
					Header:     http.Header{"Content-Type": {"application/json"}},
					Body:       io.NopCloser(strings.NewReader(responseBody)),
					Request:    request,
				}, nil
			})
			env := map[string]string{"FOUNDRY_PROJECT_ENDPOINT": "https://example.test", "AZURE_LOCATION": "eastus"}
			provider := &AgentServiceTargetProvider{
				credential:    hostedResourceErrorCredential{},
				dependencyEnv: env,
			}
			var deployErr error
			if strings.HasPrefix(mode, "container") {
				_, deployErr = provider.createAgent(t.Context(), "my-service", hostedResourceRequest("4", "4Gi"), env)
			} else {
				props, err := structpb.NewStruct(map[string]any{
					"container": map[string]any{"resources": map[string]any{"cpu": "4", "memory": "4Gi"}},
				})
				require.NoError(t, err)
				zipPath := filepath.Join(t.TempDir(), "agent.zip")
				require.NoError(t, os.WriteFile(zipPath, []byte("test-zip"), 0o600))
				_, deployErr = provider.deployHostedCodeAgent(
					t.Context(), &azdext.ServiceConfig{Name: "my-service", AdditionalProperties: props},
					&azdext.ServiceContext{Package: []*azdext.Artifact{{
						Location: zipPath, Metadata: map[string]string{"type": "code-zip", "sha256": "test-sha"},
					}}},
					func(string) {},
					agent_yaml.ContainerAgent{
						AgentDefinition: agent_yaml.AgentDefinition{Kind: agent_yaml.AgentKindHosted, Name: "foundry-agent"},
						CodeConfiguration: &agent_yaml.CodeConfiguration{
							Runtime: "python_3_13", EntryPoint: "agent.py",
						},
					}, env,
				)
			}
			require.Error(t, deployErr)
			require.Equal(t, 1, writeRequests, "deployment returned before sending the expected request: %v", deployErr)
			require.Contains(t, azdext.ErrorMessage(deployErr), `service "my-service"`)
			require.Contains(t, azdext.ErrorMessage(deployErr), `CPU="4", memory="4Gi"`)
			require.Contains(t, azdext.ErrorMessage(deployErr), resourceTierErrorMessage)
			require.Contains(t, azdext.ErrorSuggestion(deployErr), "container.resources.cpu")
		})
	}
}
