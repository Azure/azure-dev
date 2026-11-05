// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

// cspell:ignore BYOAKS

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"azureaiagent/internal/pkg/agents/agent_api"
	"azureaiagent/internal/pkg/agents/agent_yaml"
	"azureaiagent/internal/pkg/recordproxy"
	"azureaiagent/internal/synthesis"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
)

func TestStandbyPoolServiceSources(t *testing.T) {
	for _, source := range []string{"inline", "root yaml ref", "root json ref", "nested pool ref"} {
		for _, test := range []struct {
			name string
			pool any
			size *int32
			bad  bool
		}{
			{"null", nil, nil, false},
			{"empty", map[string]any{}, nil, false},
			{"null size", map[string]any{"size": nil}, nil, false},
			{"zero", map[string]any{"size": 0}, new(int32(0)), false},
			{"positive", map[string]any{"size": 3}, new(int32(3)), false},
			{"maximum", map[string]any{"size": 50}, new(int32(50)), false},
			{"negative", map[string]any{"size": -1}, nil, true},
			{"too large", map[string]any{"size": 51}, nil, true},
			{"fraction", map[string]any{"size": 3.5}, nil, true},
			{"string", map[string]any{"size": "3"}, nil, true},
			{"boolean", map[string]any{"size": true}, nil, true},
			{"unsupported control", map[string]any{"size": 3, "refill": true}, nil, true},
		} {
			t.Run(source+"/"+test.name, func(t *testing.T) {
				root := t.TempDir()
				endpoint := map[string]any{"standbyPool": test.pool}
				fields := map[string]any{
					"kind": "hosted", "name": "agent", "agentEndpoint": endpoint,
				}
				if source == "nested pool ref" {
					data, err := json.Marshal(test.pool)
					require.NoError(t, err)
					if test.pool == nil {
						// A referenced file must be an object; null remains inline.
						data = []byte(`{"size":null}`)
					}
					require.NoError(t, os.WriteFile(filepath.Join(root, "pool.json"), data, 0o600))
					endpoint["standbyPool"] = map[string]any{"$ref": "pool.json"}
				} else if source != "inline" {
					path := "agent.json"
					if source == "root yaml ref" {
						path = "agent.yaml"
					}
					// JSON is valid YAML, exercising both root file loaders.
					data, err := json.Marshal(fields)
					require.NoError(t, err)
					require.NoError(t, os.WriteFile(filepath.Join(root, path), data, 0o600))
					fields = map[string]any{"$ref": path}
				}
				svc := &azdext.ServiceConfig{
					Name: "agent", Host: foundryAgentHost, AdditionalProperties: mustStruct(t, fields),
				}
				before := proto.CloneOf(svc)
				_, err := ValidateAgentServiceDefinition(svc, root)
				require.True(t, proto.Equal(before, svc), "validation must not mutate the service")
				if test.bad {
					require.Error(t, err)
					return
				}
				require.NoError(t, err)
				definition, hosted, _, err := LoadHostedAgentDefinition(svc, root)
				require.NoError(t, err)
				require.True(t, hosted)
				apiEndpoint, _, err := agent_yaml.MapEndpointAndCard(definition.AgentEndpoint, nil)
				require.NoError(t, err)
				if test.size == nil {
					require.Nil(t, apiEndpoint.StandbyPool)
				} else {
					require.Equal(t, *test.size, *apiEndpoint.StandbyPool.Size)
				}
			})
		}
	}
}

func TestStandbyPoolSchema(t *testing.T) {
	schema, err := compiledAgentDefinitionSchema()
	require.NoError(t, err)
	for _, value := range []string{
		`null`, `{}`, `{"size":null}`, `{"size":0}`, `{"size":3}`, `{"size":50}`,
	} {
		var endpoint any
		require.NoError(t, json.Unmarshal([]byte(`{"standbyPool":`+value+`}`), &endpoint))
		require.NoError(t, schema.Validate(map[string]any{
			"kind": "hosted", "name": "agent", "agentEndpoint": endpoint,
		}), value)
	}
	for _, value := range []string{
		`{"size":-1}`, `{"size":51}`, `{"size":3.5}`, `{"size":"3"}`, `{"size":true}`,
		`[]`, `{"size":3,"refill":true}`,
	} {
		var endpoint any
		require.NoError(t, json.Unmarshal([]byte(`{"standbyPool":`+value+`}`), &endpoint))
		require.Error(t, schema.Validate(map[string]any{
			"kind": "hosted", "name": "agent", "agentEndpoint": endpoint,
		}), value)
	}
}

func TestStandbyPoolDoesNotInferHostingOrCapacity(t *testing.T) {
	for _, optIn := range []string{"", "true"} {
		t.Run("byo opt-in="+optIn, func(t *testing.T) {
			t.Setenv(synthesis.BYOAKSEnvVar, optIn)
			for _, fields := range []map[string]any{
				{"kind": "hosted", "name": "agent"},
				{"kind": "hosted", "name": "agent", "agentEndpoint": map[string]any{
					"standbyPool": map[string]any{"size": 3},
				}},
			} {
				svc := &azdext.ServiceConfig{
					Name: "agent", Host: foundryAgentHost, AdditionalProperties: mustStruct(t, fields),
				}
				require.NoError(t, RequireAgentFeatures(svc, t.TempDir()))
				definition, _, _, err := LoadHostedAgentDefinition(svc, t.TempDir())
				require.NoError(t, err)
				endpoint, _, err := agent_yaml.MapEndpointAndCard(definition.AgentEndpoint, nil)
				require.NoError(t, err)
				if _, configured := fields["agentEndpoint"]; configured {
					require.Equal(t, int32(3), *endpoint.StandbyPool.Size)
				} else {
					require.Nil(t, endpoint)
				}
			}
		})
	}
}

func standbyPoolProjectService(t *testing.T) *azdext.ServiceConfig {
	t.Helper()
	return &azdext.ServiceConfig{
		Name: "project", Host: foundryProjectHost,
		AdditionalProperties: mustStruct(t, map[string]any{
			"agentHosting": map[string]any{"clusterResourceId": "${AKS_CLUSTER_ID}"},
		}),
	}
}

func TestStandbyPoolRequiresOptedInHosting(t *testing.T) {
	for _, size := range []int32{0, 3} {
		for _, test := range []struct {
			name, optIn, want string
			uses              []string
			props             map[string]any
			secondProject     bool
		}{
			{name: "disabled", optIn: "false", want: "disabled", uses: []string{"project"}},
			{name: "empty switch", want: "disabled", uses: []string{"project"}},
			{name: "invalid switch", optIn: "invalid", want: "disabled", uses: []string{"project"}},
			{name: "BYO AKS", optIn: "true", uses: []string{"project"}},
			{name: "existing project needs live verification", optIn: "true", uses: []string{"project"},
				props: map[string]any{"endpoint": "https://example.test"}},
			{name: "unbound", optIn: "true", want: "requires BYO AKS hosting"},
			{name: "unrelated dependency", optIn: "true", uses: []string{"other"}, want: "requires BYO AKS hosting"},
			{name: "ADC", optIn: "true", uses: []string{"project"},
				props: map[string]any{}, want: "requires BYO AKS hosting"},
			{name: "empty hosting", optIn: "true", uses: []string{"project"},
				props: map[string]any{"agentHosting": map[string]any{}}, want: "requires BYO AKS hosting"},
			{name: "adopted endpoint", optIn: "true", uses: []string{"project"}, props: map[string]any{
				"endpoint": "https://example.test", "agentHosting": map[string]any{"clusterResourceId": "cluster"},
			}, want: "requires BYO AKS hosting"},
			{name: "ambiguous projects", optIn: "true", uses: []string{"project", "second"},
				secondProject: true, want: "requires BYO AKS hosting"},
		} {
			t.Run(fmt.Sprintf("%s/size=%d", test.name, size), func(t *testing.T) {
				t.Setenv(synthesis.BYOAKSEnvVar, test.optIn)
				service := &azdext.ServiceConfig{Name: "agent", Uses: test.uses}
				projectService := standbyPoolProjectService(t)
				if test.props != nil {
					projectService.AdditionalProperties = mustStruct(t, test.props)
				}
				services := map[string]*azdext.ServiceConfig{"project": projectService}
				if test.secondProject {
					services["second"] = standbyPoolProjectService(t)
				}
				endpoint := &agent_yaml.AgentEndpoint{StandbyPool: &agent_yaml.StandbyPool{Size: new(size)}}
				err := RequireStandbyPoolHosting(endpoint, service, services, t.TempDir())
				if test.want != "" {
					require.ErrorContains(t, err, test.want)
				} else {
					require.NoError(t, err)
				}
			})
		}
	}
	t.Setenv(synthesis.BYOAKSEnvVar, "false")
	for _, endpoint := range []*agent_yaml.AgentEndpoint{
		nil, {}, {StandbyPool: &agent_yaml.StandbyPool{}}, {Protocols: []string{"responses"}},
	} {
		require.NoError(t, RequireStandbyPoolHosting(endpoint, nil, nil, ""))
	}
}

func TestStandbyPoolExistingARMHosting(t *testing.T) {
	const projectID = "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.CognitiveServices/accounts/acct/projects/proj"
	const endpoint = "https://acct.services.ai.azure.com/api/projects/proj"
	const clusterID = "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.ContainerService/managedClusters/aks"
	for _, test := range []struct {
		name, projectEndpoint, hostingType, clusterID, want string
		status                                              int
	}{
		{name: "AKS", projectEndpoint: endpoint, hostingType: "ManagedCluster", clusterID: clusterID},
		{name: "ADC", projectEndpoint: endpoint, want: "no verified ManagedCluster"},
		{name: "mismatched project", projectEndpoint: endpoint + "-other", want: "does not identify"},
		{name: "wrong hosting", projectEndpoint: endpoint, hostingType: "ADC", clusterID: clusterID,
			want: "no verified ManagedCluster"},
		{name: "invalid cluster", projectEndpoint: endpoint, hostingType: "ManagedCluster", clusterID: "/invalid",
			want: "no verified ManagedCluster"},
		{name: "denied", projectEndpoint: endpoint, status: http.StatusForbidden, want: "read project"},
	} {
		t.Run(test.name, func(t *testing.T) {
			var calls []string
			transport := hostedResourceErrorTransport(func(request *http.Request) (*http.Response, error) {
				calls = append(calls, request.Method+" "+request.URL.Path)
				require.Equal(t, http.MethodGet, request.Method)
				properties := map[string]any{}
				if strings.HasSuffix(request.URL.Path, "/projects/proj") {
					require.Equal(t, "2025-06-01", request.URL.Query().Get("api-version"))
					properties["endpoints"] = map[string]any{"AI Foundry API": test.projectEndpoint}
				} else {
					require.Equal(t, "2026-07-15-preview", request.URL.Query().Get("api-version"))
					properties["agentHostingConfigurations"] = []any{map[string]any{
						"hostingType": test.hostingType, "clusterResourceId": test.clusterID,
					}}
				}
				data, err := json.Marshal(map[string]any{"properties": properties})
				require.NoError(t, err)
				status := test.status
				if status == 0 {
					status = http.StatusOK
				}
				return &http.Response{StatusCode: status,
					Header: http.Header{"Content-Type": {"application/json"}},
					Body:   io.NopCloser(strings.NewReader(string(data))), Request: request}, nil
			})
			options := &arm.ClientOptions{ClientOptions: policy.ClientOptions{Transport: &http.Client{Transport: transport}}}
			err := verifyStandbyPoolARMHosting(t.Context(), hostedResourceErrorCredential{}, options, projectID, endpoint)
			if test.want == "" {
				require.NoError(t, err)
				require.Len(t, calls, 2)
			} else {
				require.ErrorContains(t, err, test.want)
				if test.name == "mismatched project" || test.name == "denied" {
					require.Len(t, calls, 1)
				}
			}
		})
	}
}

func TestStandbyPoolProvisionedHosting(t *testing.T) {
	t.Setenv(synthesis.BYOAKSEnvVar, "true")
	const projectID = "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.CognitiveServices/accounts/acct/projects/proj"
	const endpoint = "https://acct.services.ai.azure.com/api/projects/proj"
	const clusterID = "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.ContainerService/managedClusters/aks"
	previousCredential, previousTransport := newStandbyPoolCredential, recordproxy.Transport
	t.Cleanup(func() {
		newStandbyPoolCredential, recordproxy.Transport = previousCredential, previousTransport
	})
	newStandbyPoolCredential = func(tenant string) (azcore.TokenCredential, error) {
		require.Equal(t, "00000000-0000-0000-0000-000000000000", tenant)
		return hostedResourceErrorCredential{}, nil
	}
	for _, adopted := range []bool{false, true} {
		for _, size := range []int32{0, 1} {
			for _, test := range []struct {
				name, want string
				aks        bool
				deniedPath string
			}{
				{name: "verified AKS", aks: true},
				{name: "older provider silently created ADC", want: "no verified ManagedCluster"},
				{name: "project read denied", deniedPath: projectID, want: "read project"},
				{name: "account read denied", deniedPath: strings.TrimSuffix(projectID, "/projects/proj"),
					want: "read account hosting configuration"},
			} {
				t.Run(fmt.Sprintf("adopted=%t/size=%d/%s", adopted, size, test.name), func(t *testing.T) {
					service := &azdext.ServiceConfig{Name: "agent", Uses: []string{"project"}}
					dependency := standbyPoolProjectService(t)
					if adopted {
						dependency.AdditionalProperties = mustStruct(t, map[string]any{"endpoint": endpoint})
					}
					client := newEndpointsTestClient(t, t.TempDir(), map[string]string{
						"AZURE_AI_PROJECT_ID": projectID,
					}, &stubAccountServer{})
					var calls []string
					recordproxy.Transport = hostedResourceErrorTransport(func(request *http.Request) (*http.Response, error) {
						require.Equal(t, http.MethodGet, request.Method, "hosting verification must be read-only")
						calls = append(calls, request.URL.Path)
						properties := map[string]any{"endpoints": map[string]any{"AI Foundry API": endpoint}}
						if request.URL.Path != projectID {
							properties = map[string]any{}
							if test.aks {
								properties["agentHostingConfigurations"] = []any{map[string]any{
									"hostingType": "ManagedCluster", "clusterResourceId": clusterID,
								}}
							}
						}
						status := http.StatusOK
						if request.URL.Path == test.deniedPath {
							status = http.StatusForbidden
						}
						data, err := json.Marshal(map[string]any{"properties": properties})
						require.NoError(t, err)
						return &http.Response{StatusCode: status,
							Header: http.Header{"Content-Type": {"application/json"}},
							Body:   io.NopCloser(strings.NewReader(string(data))), Request: request}, nil
					})
					err := VerifyStandbyPoolHosting(t.Context(), client,
						&agent_yaml.AgentEndpoint{StandbyPool: &agent_yaml.StandbyPool{Size: new(size)}},
						service, map[string]*azdext.ServiceConfig{"project": dependency}, t.TempDir(), endpoint, "test",
					)
					if test.want == "" {
						require.NoError(t, err)
					} else {
						require.ErrorContains(t, err, test.want)
					}
					require.NotEmpty(t, calls, "a local hosting declaration cannot bypass ARM verification")
				})
			}
		}
	}
	for _, endpoint := range []*agent_yaml.AgentEndpoint{nil, {}, {StandbyPool: &agent_yaml.StandbyPool{}}} {
		require.NoError(t, VerifyStandbyPoolHosting(t.Context(), nil, endpoint, nil, nil, "", "", ""))
	}
}

func TestStandbyPoolHostingProjectReference(t *testing.T) {
	t.Setenv(synthesis.BYOAKSEnvVar, "true")
	root := t.TempDir()
	path := filepath.Join(root, "project.yaml")
	require.NoError(t, os.WriteFile(path, []byte("agentHosting:\n  clusterResourceId: ${AKS_CLUSTER_ID}\n"), 0o600))
	service := &azdext.ServiceConfig{Name: "agent", Uses: []string{"project"}}
	dependency := &azdext.ServiceConfig{
		Name: "project", Host: foundryProjectHost,
		AdditionalProperties: mustStruct(t, map[string]any{"$ref": "project.yaml"}),
	}
	endpoint := &agent_yaml.AgentEndpoint{StandbyPool: &agent_yaml.StandbyPool{Size: new(int32(3))}}
	require.NoError(t, RequireStandbyPoolHosting(
		endpoint, service, map[string]*azdext.ServiceConfig{"project": dependency}, root,
	))

	agentPath := filepath.Join(root, "agent.json")
	agentData := []byte(`{"kind":"hosted","name":"agent","agentEndpoint":{"standbyPool":{"size":0}}}`)
	require.NoError(t, os.WriteFile(agentPath, agentData, 0o600))
	service.Host = foundryAgentHost
	service.AdditionalProperties = mustStruct(t, map[string]any{"$ref": "agent.json"})
	client, projectServer := newEndpointsTestClientWithProjectServer(t, root, nil)
	projectServer.project.Services = map[string]*azdext.ServiceConfig{"agent": service, "project": dependency}
	provider := &AgentServiceTargetProvider{azdClient: client}
	require.NoError(t, provider.validateDeploymentServiceConfig(t.Context(), service))
	require.Nil(t, provider.credential)
	require.Nil(t, provider.env)
	after, err := os.ReadFile(agentPath)
	require.NoError(t, err)
	require.Equal(t, agentData, after)
}

func TestStandbyPoolDeploymentPatch(t *testing.T) {
	for _, status := range []int{http.StatusOK, http.StatusBadRequest} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			original := recordproxy.Transport
			t.Cleanup(func() { recordproxy.Transport = original })
			requests := 0
			recordproxy.Transport = hostedResourceErrorTransport(func(request *http.Request) (*http.Response, error) {
				requests++
				require.Equal(t, http.MethodPatch, request.Method, "no version creation or cleanup")
				require.Equal(t, "/agents/deployed-agent", request.URL.Path)
				require.Equal(t, agent_api.StandbyPoolAPIVersion, request.URL.Query().Get("api-version"))
				body, err := io.ReadAll(request.Body)
				require.NoError(t, err)
				require.JSONEq(t, `{"agent_endpoint":{"standby_pool":{"size":0}}}`, string(body))
				return &http.Response{
					StatusCode: status,
					Header:     http.Header{"Content-Type": {"application/json"}},
					Body:       io.NopCloser(strings.NewReader(`{"name":"deployed-agent"}`)),
					Request:    request,
				}, nil
			})
			provider := &AgentServiceTargetProvider{credential: hostedResourceErrorCredential{}}
			err := provider.patchAgentEndpointFields(t.Context(), "deployed-agent",
				&agent_api.AgentEndpoint{StandbyPool: &agent_api.StandbyPool{Size: new(int32(0))}}, nil,
				map[string]string{"FOUNDRY_PROJECT_ENDPOINT": "https://example.test"})
			if status == http.StatusOK {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}
			require.Equal(t, 1, requests)
		})
	}
}

func TestStandbyPoolDeployVerifiesHostingAndReportsPartialSuccess(t *testing.T) {
	t.Setenv(synthesis.BYOAKSEnvVar, "true")
	originalCredential := newStandbyPoolCredential
	t.Cleanup(func() { newStandbyPoolCredential = originalCredential })
	newStandbyPoolCredential = func(string) (azcore.TokenCredential, error) {
		return hostedResourceErrorCredential{}, nil
	}
	for _, verified := range []bool{false, true} {
		for _, mode := range []string{"container", "code"} {
			t.Run(fmt.Sprintf("%s/verified=%t", mode, verified), func(t *testing.T) {
				root := t.TempDir()
				fields := map[string]any{
					"kind": "hosted", "name": "agent",
					"agentEndpoint": map[string]any{"standbyPool": map[string]any{"size": 3}},
				}
				serviceContext := &azdext.ServiceContext{Publish: []*azdext.Artifact{{
					Kind:         azdext.ArtifactKind_ARTIFACT_KIND_CONTAINER,
					LocationKind: azdext.LocationKind_LOCATION_KIND_REMOTE,
					Location:     "registry.example/agent:latest",
				}}}
				if mode == "code" {
					fields["codeConfiguration"] = map[string]any{
						"runtime": "python_3_13", "entryPoint": "agent.py",
					}
					path := filepath.Join(root, "agent.zip")
					require.NoError(t, os.WriteFile(path, []byte("test-zip"), 0o600))
					serviceContext.Package = []*azdext.Artifact{{
						Location: path, Metadata: map[string]string{"type": "code-zip", "sha256": "test-sha"},
					}}
				}
				service := &azdext.ServiceConfig{
					Name: "agent", Host: foundryAgentHost, AdditionalProperties: mustStruct(t, fields),
					Uses: []string{"project"},
				}
				resource, err := arm.ParseResourceID("/subscriptions/sub/resourceGroups/rg/providers/" +
					"Microsoft.CognitiveServices/accounts/account/projects/project")
				require.NoError(t, err)
				provider := &AgentServiceTargetProvider{
					azdClient: newEndpointsTestClient(t, root, map[string]string{
						"FOUNDRY_PROJECT_ENDPOINT": "https://example.test", "AZURE_AI_PROJECT_ID": resource.String(),
						"AZURE_LOCATION": "eastus",
					}, &stubAccountServer{}),
					projectPath: root, servicePath: root, serviceConfig: service,
					deployContextReady: true, env: &azdext.Environment{Name: "test-env"},
					foundryProject: resource, credential: hostedResourceErrorCredential{},
					projectServices: map[string]*azdext.ServiceConfig{"project": standbyPoolProjectService(t)},
				}
				var calls []string
				original := recordproxy.Transport
				t.Cleanup(func() { recordproxy.Transport = original })
				recordproxy.Transport = hostedResourceErrorTransport(func(request *http.Request) (*http.Response, error) {
					calls = append(calls, request.Method+" "+request.URL.Path)
					status := http.StatusOK
					body := `{"name":"agent","version":"2","status":"active","definition":{"kind":"hosted"}}`
					switch {
					case strings.HasSuffix(request.URL.Path, "/accounts/account/projects/project"):
						body = `{"properties":{"endpoints":{"AI Foundry API":"https://example.test"}}}`
					case strings.HasSuffix(request.URL.Path, "/accounts/account"):
						body = `{"properties":{"agentHostingConfigurations":[{"hostingType":"ManagedCluster",
						"clusterResourceId":"/subscriptions/sub/resourceGroups/rg/providers/Microsoft.ContainerService/managedClusters/aks"}]}}`
						if !verified {
							body = `{"properties":{}}`
						}
					case request.Method == http.MethodPatch:
						require.Equal(t, agent_api.StandbyPoolAPIVersion, request.URL.Query().Get("api-version"))
						payload, readErr := io.ReadAll(request.Body)
						require.NoError(t, readErr)
						require.JSONEq(t, `{"agent_endpoint":{"standby_pool":{"size":3}}}`, string(payload))
						status = http.StatusBadRequest
						body = `{"error":{"code":"invalid_request","message":"standby not deployed"}}`
					case request.Method == http.MethodPost:
						require.Equal(t, "v1", request.URL.Query().Get("api-version"))
						if mode == "code" {
							body = `{"name":"agent","versions":{"latest":{"version":"2","status":"active"}}}`
						}
					case request.Method == http.MethodGet && request.URL.Path == "/agents/agent":
						body = `{"name":"agent","versions":{"latest":{"version":"1","status":"active"}}}`
					default:
						require.Equal(t, http.MethodGet, request.Method, "no cleanup after a failed endpoint PATCH")
						require.Equal(t, "/agents/agent/versions/2", request.URL.Path)
					}
					return &http.Response{
						StatusCode: status,
						Header:     http.Header{"Content-Type": {"application/json"}},
						Body:       io.NopCloser(strings.NewReader(body)), Request: request,
					}, nil
				})
				stderr, err := os.CreateTemp(root, "stderr-*.txt")
				require.NoError(t, err)
				originalStderr := os.Stderr
				os.Stderr = stderr
				t.Cleanup(func() { os.Stderr = originalStderr; require.NoError(t, stderr.Close()) })

				result, err := provider.Deploy(t.Context(), service, serviceContext, nil, func(string) {})

				if !verified {
					require.ErrorContains(t, err, "no verified ManagedCluster")
					require.Nil(t, result)
					require.Equal(t, []string{
						"GET " + resource.String(), "GET " + strings.TrimSuffix(resource.String(), "/projects/project"),
					}, calls, "an older provider's ADC project must not receive agent management writes")
					return
				}
				require.ErrorContains(t, err, "standby not deployed")
				require.Nil(t, result)
				data, err := os.ReadFile(stderr.Name())
				require.NoError(t, err)
				require.Contains(t, string(data), `Agent "agent" version 2 deployed`)
				require.Contains(t, string(data), "endpoint/card configuration was not saved")
				require.Contains(t, string(data), "No deployed resources were deleted")
				require.Contains(t, string(data), "azd ai agent endpoint update")
				postPath := "/agents/agent/versions"
				if mode == "code" {
					postPath = "/agents/agent"
				}
				require.Equal(t, []string{
					"GET " + resource.String(), "GET " + strings.TrimSuffix(resource.String(), "/projects/project"),
					"GET /agents/agent", "POST " + postPath, "GET /agents/agent/versions/2", "PATCH /agents/agent",
				}, calls)
			})
		}
	}
}

func TestStandbyPoolRejectedBeforeDeploymentMutations(t *testing.T) {
	for _, entry := range []string{"package", "publish", "deploy"} {
		for _, enabled := range []string{"false", "true"} {
			t.Run(entry+"/"+enabled, func(t *testing.T) {
				t.Setenv(synthesis.BYOAKSEnvVar, enabled)
				service := &azdext.ServiceConfig{
					Name: "agent", Host: foundryAgentHost, AdditionalProperties: mustStruct(t, map[string]any{
						"kind": "hosted", "name": "agent",
						"agentEndpoint": map[string]any{"standbyPool": map[string]any{"size": 0}},
					}),
				}
				before := proto.CloneOf(service)
				provider := &AgentServiceTargetProvider{projectServices: map[string]*azdext.ServiceConfig{}}
				var err error
				switch entry {
				case "package":
					_, err = provider.Package(t.Context(), service, nil, func(string) {})
				case "publish":
					_, err = provider.Publish(t.Context(), service, nil, nil, nil, func(string) {})
				case "deploy":
					_, err = provider.Deploy(t.Context(), service, nil, nil, func(string) {})
				}
				if enabled == "true" {
					require.ErrorContains(t, err, "requires BYO AKS hosting")
				} else {
					require.ErrorContains(t, err, "disabled")
				}
				require.Nil(t, provider.credential)
				require.Nil(t, provider.env)
				require.True(t, proto.Equal(before, service))
			})
		}
	}
}

func TestStandbyPoolInvalidDeployInputsBeforeAuthentication(t *testing.T) {
	for _, entry := range []string{"package", "publish", "deploy"} {
		t.Run(entry, func(t *testing.T) {
			service := &azdext.ServiceConfig{
				Name: "agent", Host: foundryAgentHost, AdditionalProperties: mustStruct(t, map[string]any{
					"kind": "hosted", "name": "agent",
					"agentEndpoint": map[string]any{"standbyPool": map[string]any{"size": 51}},
				}),
			}
			before := proto.CloneOf(service)
			provider := &AgentServiceTargetProvider{}
			var err error
			switch entry {
			case "package":
				_, err = provider.Package(t.Context(), service, nil, func(string) {})
			case "publish":
				_, err = provider.Publish(t.Context(), service, nil, nil, nil, func(string) {})
			case "deploy":
				_, err = provider.Deploy(t.Context(), service, nil, nil, func(string) {})
			}
			require.ErrorContains(t, err, "between 0 and 50")
			require.Nil(t, provider.credential)
			require.Nil(t, provider.env)
			require.True(t, proto.Equal(before, service))
		})
	}
}
