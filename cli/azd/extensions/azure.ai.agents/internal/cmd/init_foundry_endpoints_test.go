// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"azureaiagent/internal/pkg/recordproxy"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/require"
)

func TestSelectedFoundryProjectCustomSubdomain(t *testing.T) {
	const (
		projectID = "/subscriptions/sub/resourceGroups/rg/providers/" +
			"Microsoft.CognitiveServices/accounts/resource-account/projects/project"
		endpoint = "https://custom-dns.services.ai.azure.com/api/projects/project"
		envName  = "test"
	)
	for _, prompt := range []bool{false, true} {
		for _, explicit := range []bool{false, true} {
			t.Run(fmt.Sprintf("prompt=%v/explicit=%v", prompt, explicit), func(t *testing.T) {
				var requests []string
				previous := recordproxy.Transport
				t.Cleanup(func() { recordproxy.Transport = previous })
				recordproxy.Transport = roundTripFunc(func(request *http.Request) (*http.Response, error) {
					require.Equal(t, "management.azure.com", request.URL.Host)
					requests = append(requests, request.URL.Path)
					body := `{"id":"` + projectID + `","name":"resource-account/project","location":"eastus",` +
						`"identity":{"type":"SystemAssigned","principalId":"project-principal"},` +
						`"properties":{"endpoints":{"AI Foundry API":"` + endpoint + `"}}}`
					switch request.URL.Path {
					case "/subscriptions/sub/resources":
						body = `{"value":[{"id":"` + projectID + `","location":"eastus"}]}`
					case strings.TrimSuffix(projectID, "/projects/project"):
						body = `{"properties":{"customSubDomainName":"custom-dns",` +
							`"networkInjections":[{"scenario":"agent"}]}}`
					default:
						require.Equal(t, projectID, request.URL.Path)
					}
					return &http.Response{
						StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/json"}},
						Body: io.NopCloser(strings.NewReader(body)), Request: request,
					}, nil
				})
				env := &testEnvironmentServiceServer{
					values: map[string]map[string]string{envName: {}},
				}
				prompts := &helpersPromptServer{}
				client := newTestAzdClient(t, env, &testWorkflowServiceServer{}, prompts)
				scope := &azdext.AzureContext{Scope: &azdext.AzureScope{SubscriptionId: "sub"}}
				id := ""
				if explicit {
					id = projectID
				}
				var project *FoundryProjectInfo
				var err error
				if prompt {
					project, err = selectPromptFoundryProject(t.Context(), client, responseTestCredential{},
						scope, envName, id, explicit, nil)
					require.NoError(t, err)
					require.NoError(t, setPromptFoundryProjectEnv(t.Context(), client, envName, project))
				} else {
					project, err = selectFoundryProject(t.Context(), client, responseTestCredential{},
						scope, envName, "sub", id, "", true, false, true, nil)
					require.NoError(t, err)
				}
				require.NotNil(t, project)
				require.Equal(t, projectID, project.ResourceId)
				require.Equal(t, "resource-account", project.AccountName)
				require.Equal(t, "project", project.ProjectName)
				require.Equal(t, endpoint, project.Endpoint())
				require.Equal(t, "https://custom-dns.openai.azure.com/", project.openAIEndpoint())
				require.Equal(t, "project-principal", project.PrincipalId)
				require.True(t, project.NetworkInjected)
				require.NoError(t, recordFoundryProjectEnv(t.Context(), client, envName, project))
				require.Equal(t, projectID, env.values[envName]["AZURE_AI_PROJECT_ID"])
				require.Equal(t, "resource-account", env.values[envName]["AZURE_AI_ACCOUNT_NAME"])
				require.Equal(t, endpoint, env.values[envName]["FOUNDRY_PROJECT_ENDPOINT"])
				require.Equal(t, "https://custom-dns.openai.azure.com/", env.values[envName]["AZURE_OPENAI_ENDPOINT"])
				wantRequests := []string{projectID, strings.TrimSuffix(projectID, "/projects/project")}
				if !explicit {
					wantRequests = append([]string{"/subscriptions/sub/resources"}, wantRequests...)
				}
				require.Equal(t, wantRequests, requests)
			})
		}
	}
}

func TestFoundryProjectMetadataFailureDoesNotPersistEndpoints(t *testing.T) {
	const (
		projectID = "/subscriptions/sub/resourceGroups/rg/providers/" +
			"Microsoft.CognitiveServices/accounts/resource-account/projects/project"
		envName = "test"
	)
	previous := recordproxy.Transport
	t.Cleanup(func() { recordproxy.Transport = previous })
	recordproxy.Transport = roundTripFunc(func(request *http.Request) (*http.Response, error) {
		body := `{"properties":{"endpoints":{"AI Foundry API":"https://example.com/api/projects/project"}}}`
		if !strings.HasSuffix(request.URL.Path, "/projects/project") {
			body = `{"properties":{"customSubDomainName":"custom-dns"}}`
		}
		return &http.Response{
			StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/json"}},
			Body: io.NopCloser(strings.NewReader(body)), Request: request,
		}, nil
	})
	env := &testEnvironmentServiceServer{values: map[string]map[string]string{envName: {}}}
	client := newTestAzdClient(t, env, &testWorkflowServiceServer{}, &helpersPromptServer{})
	selected, err := selectFoundryProject(t.Context(), client, responseTestCredential{},
		&azdext.AzureContext{Scope: &azdext.AzureScope{SubscriptionId: "sub"}},
		envName, "sub", projectID, "", true, false, true, nil)
	require.ErrorContains(t, err, "invalid ARM-advertised")
	require.Nil(t, selected)
	require.Empty(t, env.values[envName])
}

func TestExistingProjectInfraEjectUsesARMEndpoint(t *testing.T) {
	const (
		projectID = "/subscriptions/sub/resourceGroups/rg/providers/" +
			"Microsoft.CognitiveServices/accounts/resource-account/projects/project"
		endpoint = "https://custom-dns.services.ai.azure.com/api/projects/project"
	)
	for _, provider := range []string{"bicep", "terraform"} {
		for _, tt := range []struct {
			name, configured, advertised, wantError string
			accountStatus                           int
			noLookup                                bool
		}{
			{name: "custom subdomain", configured: endpoint},
			{name: "legacy project path", configured: "https://custom-dns.services.ai.azure.com/projects/project"},
			{name: "other account", configured: "https://other.services.ai.azure.com/api/projects/project",
				wantError: "does not match"},
			{name: "other project", configured: "https://custom-dns.services.ai.azure.com/api/projects/other",
				wantError: "does not match", noLookup: true},
			{name: "invalid URL", configured: endpoint + "?sig=secret",
				wantError: "does not match", noLookup: true},
			{name: "invalid metadata", configured: endpoint, advertised: "https://example.com/api/projects/project",
				wantError: "invalid ARM-advertised"},
			{name: "account read denied", configured: endpoint, accountStatus: http.StatusForbidden,
				wantError: "read Foundry account"},
		} {
			t.Run(provider+"/"+tt.name, func(t *testing.T) {
				root := t.TempDir()
				t.Setenv("AZD_EXEC_PROJECT_DIR", root)
				raw := "name: test\nservices:\n  project:\n    host: azure.ai.project\n" +
					"    endpoint: " + tt.configured + "\n"
				mustWriteFile(t, filepath.Join(root, "azure.yaml"), raw)
				env := &testEnvironmentServiceServer{
					current: &azdext.Environment{Name: "test"},
					values: map[string]map[string]string{"test": {
						"AZURE_AI_PROJECT_ID": projectID, "FOUNDRY_PROJECT_ENDPOINT": tt.configured,
					}},
				}
				before := maps.Clone(env.values["test"])
				client := newTestAzdClient(t, env, &testWorkflowServiceServer{})
				var requests []string
				previous := recordproxy.Transport
				t.Cleanup(func() { recordproxy.Transport = previous })
				recordproxy.Transport = roundTripFunc(func(request *http.Request) (*http.Response, error) {
					require.Equal(t, http.MethodGet, request.Method)
					requests = append(requests, request.URL.Path)
					advertised := tt.advertised
					if advertised == "" {
						advertised = endpoint
					}
					code := http.StatusOK
					body := `{"properties":{"endpoints":{"AI Foundry API":"` + advertised + `"}}}`
					if request.URL.Path != projectID {
						require.Equal(t, strings.TrimSuffix(projectID, "/projects/project"), request.URL.Path)
						body = `{"properties":{"customSubDomainName":"custom-dns"}}`
						if tt.accountStatus != 0 {
							code = tt.accountStatus
							body = `{"error":{"code":"AuthorizationFailed","message":"Access denied"}}`
						}
					}
					return &http.Response{
						StatusCode: code, Header: http.Header{"Content-Type": {"application/json"}},
						Body: io.NopCloser(strings.NewReader(body)), Request: request,
					}, nil
				})
				lookup := func(ctx context.Context, id string) (*FoundryProjectInfo, error) {
					require.False(t, tt.noLookup, "invalid local inputs must fail before authentication")
					require.Equal(t, projectID, id)
					return getFoundryProject(ctx, responseTestCredential{}, "sub", id)
				}
				var err error
				withCapturedStdout(t, func() {
					err = ejectInfraAfterInitWithProjectLookup(t.Context(), provider, client, lookup)
				})
				if tt.wantError != "" {
					require.ErrorContains(t, err, tt.wantError)
					require.NotContains(t, err.Error(), "sig=secret")
					require.NoDirExists(t, filepath.Join(root, "infra"))
					//nolint:gosec // Test-owned path.
					actual, readErr := os.ReadFile(filepath.Join(root, "azure.yaml"))
					require.NoError(t, readErr)
					require.Equal(t, raw, string(actual))
				} else {
					require.NoError(t, err)
					file := "main.tfvars.json"
					if provider == "bicep" {
						file = "main.parameters.json"
						require.FileExists(t, filepath.Join(root, "infra", "main.bicep"))
					} else {
						require.FileExists(t, filepath.Join(root, "infra", "main.tf"))
					}
					//nolint:gosec // Test-owned path.
					data, readErr := os.ReadFile(filepath.Join(root, "infra", file))
					require.NoError(t, readErr)
					var values map[string]any
					require.NoError(t, json.Unmarshal(data, &values))
					if provider == "bicep" {
						require.Contains(t, values, "parameters")
						require.Contains(t, string(data), "${AZURE_AI_PROJECT_ID}")
						require.Contains(t, string(data), "${FOUNDRY_PROJECT_ENDPOINT}")
					} else {
						require.Equal(t, "${AZURE_AI_PROJECT_ID}", values["project_resource_id"])
						require.Equal(t, "${FOUNDRY_PROJECT_ENDPOINT}", values["project_endpoint"])
					}
				}
				require.Equal(t, before, env.values["test"])
				require.Empty(t, env.setKeys)
				if tt.noLookup {
					require.Empty(t, requests)
				} else {
					require.Equal(t, []string{projectID, strings.TrimSuffix(projectID, "/projects/project")}, requests)
				}
			})
		}
	}
}
