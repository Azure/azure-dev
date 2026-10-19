// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"

	"azure.ai.projects/internal/provisioning"
	"azure.ai.projects/internal/synthesis"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/structpb"
)

func TestLoadProjectServiceConfig(t *testing.T) {
	t.Parallel()

	deployment := synthesis.Deployment{
		Name: "gpt-4.1-mini",
		Model: synthesis.DeploymentModel{
			Format:  "OpenAI",
			Name:    "gpt-4.1-mini",
			Version: "2025-04-14",
		},
		Sku: synthesis.DeploymentSku{
			Name:     "GlobalStandard",
			Capacity: 10,
		},
	}
	props := mustProjectProperties(t, map[string]any{
		"endpoint": "https://example.services.ai.azure.com/" +
			"api/projects/example",
		"deployments": []any{
			map[string]any{
				"name": deployment.Name,
				"model": map[string]any{
					"format":  deployment.Model.Format,
					"name":    deployment.Model.Name,
					"version": deployment.Model.Version,
				},
				"sku": map[string]any{
					"name":     deployment.Sku.Name,
					"capacity": deployment.Sku.Capacity,
				},
			},
		},
	})

	tests := []struct {
		name     string
		service  *azdext.ServiceConfig
		wantSeen bool
	}{
		{
			name: "inline properties",
			service: &azdext.ServiceConfig{
				Host:                 aiProjectHost,
				AdditionalProperties: props,
			},
			wantSeen: true,
		},
		{
			name: "legacy config",
			service: &azdext.ServiceConfig{
				Host:   aiProjectHost,
				Config: props,
			},
			wantSeen: true,
		},
		{
			name: "unrelated host",
			service: &azdext.ServiceConfig{
				Host: "azure.ai.agent",
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			cfg, found, err := loadProjectServiceConfig(
				map[string]*azdext.ServiceConfig{
					"service": test.service,
				},
				"",
			)
			require.NoError(t, err)
			assert.Equal(t, test.wantSeen, found)
			if !found {
				return
			}
			require.Len(t, cfg.Deployments, 1)
			assert.Equal(t, deployment, cfg.Deployments[0])
		})
	}
}

func TestLoadProjectServiceConfigRejectsDuplicates(t *testing.T) {
	t.Parallel()

	services := map[string]*azdext.ServiceConfig{
		"zeta":  {Host: aiProjectHost},
		"alpha": {Host: aiProjectHost},
	}

	_, _, err := loadProjectServiceConfig(services, "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "alpha, zeta")
}

func TestDefaultModelDeploymentName(t *testing.T) {
	t.Parallel()

	service := map[string]any{
		"deployments": []any{map[string]any{"name": "yaml-chat"}},
	}
	encoded, err := provisioning.EncodeProjectDeployments([]synthesis.Deployment{{
		Name:  "env-chat",
		Model: synthesis.DeploymentModel{Name: "gpt-4.1", Format: "OpenAI", Version: "1"},
		Sku:   synthesis.DeploymentSku{Name: "Standard", Capacity: 1},
	}, {
		Name:  "env-embedding",
		Model: synthesis.DeploymentModel{Name: "text-embedding-3-small", Format: "OpenAI", Version: "1"},
		Sku:   synthesis.DeploymentSku{Name: "Standard", Capacity: 1},
	}})
	require.NoError(t, err)

	name, err := defaultModelDeploymentName(encoded, service)
	require.NoError(t, err)
	assert.Equal(t, "env-chat", name)

	name, err = defaultModelDeploymentName(
		`[{"name":"raw-chat","model":{"name":"gpt-4.1","format":"OpenAI","version":"1"},`+
			`"sku":{"name":"Standard","capacity":1}}]`, service,
	)
	require.NoError(t, err)
	assert.Equal(t, "raw-chat", name)

	name, err = defaultModelDeploymentName("[]", service)
	require.NoError(t, err)
	assert.Empty(t, name)

	name, err = defaultModelDeploymentName("  ", service)
	require.NoError(t, err)
	assert.Equal(t, "yaml-chat", name)

	_, err = defaultModelDeploymentName("{", service)
	require.ErrorContains(t, err, projectDeploymentsEnvKey)
}

func TestFirstProjectDeploymentName(t *testing.T) {
	t.Parallel()

	assert.Equal(
		t,
		"first",
		firstProjectDeploymentName(map[string]any{
			"deployments": []any{
				map[string]any{"name": "first"},
				map[string]any{"name": "second"},
			},
		}),
	)
	assert.Empty(t, firstProjectDeploymentName(map[string]any{}))
	assert.Empty(t, firstProjectDeploymentName(map[string]any{
		"deployments": []any{"invalid"},
	}))
}

func TestProjectLifecycleHandlerWritesDeployments(t *testing.T) {
	t.Setenv("AZD_NO_PROMPT", "true")

	envServer := &recordingProjectEnvironmentServer{
		envName: "dev",
	}
	client := newProjectEnvironmentClient(t, envServer)
	props := mustProjectProperties(t, map[string]any{
		"deployments": []any{
			map[string]any{
				"name": "gpt-4.1-mini",
				"model": map[string]any{
					"format":  "OpenAI",
					"name":    "gpt-4.1-mini",
					"version": "2025-04-14",
				},
				"sku": map[string]any{
					"name":     "GlobalStandard",
					"capacity": 10,
				},
			},
		},
	})

	err := projectLifecycleHandler(
		t.Context(),
		client,
		&azdext.ProjectEventArgs{
			Project: &azdext.ProjectConfig{
				Services: map[string]*azdext.ServiceConfig{
					"project": {
						Host:                 aiProjectHost,
						AdditionalProperties: props,
					},
				},
			},
		},
	)
	require.NoError(t, err)

	envServer.mu.Lock()
	defer envServer.mu.Unlock()
	assert.Equal(t, "dev", envServer.envNameSet)
	assert.Equal(t, "gpt-4.1-mini", envServer.values["AZURE_AI_MODEL_DEPLOYMENT_NAME"])
	assert.Equal(
		t,
		`[{\"name\":\"gpt-4.1-mini\",`+
			`\"model\":{\"name\":\"gpt-4.1-mini\",`+
			`\"format\":\"OpenAI\",\"version\":\"2025-04-14\"},`+
			`\"sku\":{\"name\":\"GlobalStandard\",`+
			`\"capacity\":10}}]`,
		envServer.values[projectDeploymentsEnvKey],
	)
}

func TestProjectLifecycleHandlerClearsEmptyDeployments(t *testing.T) {
	t.Parallel()

	envServer := &recordingProjectEnvironmentServer{
		envName: "dev",
	}
	client := newProjectEnvironmentClient(t, envServer)

	err := projectLifecycleHandler(
		t.Context(),
		client,
		&azdext.ProjectEventArgs{
			Project: &azdext.ProjectConfig{
				Services: map[string]*azdext.ServiceConfig{
					"project": {Host: aiProjectHost},
				},
			},
		},
	)
	require.NoError(t, err)

	envServer.mu.Lock()
	defer envServer.mu.Unlock()
	assert.Equal(t, "[]", envServer.value)
}

func TestProjectLifecycleHandlerResolvesDeploymentRefs(t *testing.T) {
	t.Setenv("AZD_NO_PROMPT", "true")
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "azure.yaml"), []byte("name: test\n"), 0600))
	require.NoError(t, os.WriteFile(
		filepath.Join(root, "deployment.yaml"),
		[]byte("name: gpt-4o\nmodel: {name: gpt-4o, format: OpenAI, version: '2024-08-06'}\n"+
			"sku: {name: Standard, capacity: 10}\n"),
		0600,
	))

	envServer := &recordingProjectEnvironmentServer{envName: "dev"}
	client := newProjectEnvironmentClient(t, envServer)
	err := projectLifecycleHandler(
		t.Context(),
		client,
		&azdext.ProjectEventArgs{
			Project: &azdext.ProjectConfig{
				Path: root,
				Services: map[string]*azdext.ServiceConfig{
					"project": {
						Host: aiProjectHost,
						AdditionalProperties: mustProjectProperties(t, map[string]any{
							"deployments": []any{
								map[string]any{"$ref": "./deployment.yaml"},
							},
						}),
					},
				},
			},
		},
	)
	require.NoError(t, err)

	envServer.mu.Lock()
	defer envServer.mu.Unlock()
	assert.Contains(t, envServer.values[projectDeploymentsEnvKey], `\"name\":\"gpt-4o\"`)
}

func mustProjectProperties(
	t *testing.T,
	value map[string]any,
) *structpb.Struct {
	t.Helper()

	props, err := structpb.NewStruct(value)
	require.NoError(t, err)
	return props
}

type recordingProjectEnvironmentServer struct {
	azdext.UnimplementedEnvironmentServiceServer

	mu          sync.Mutex
	envName     string
	envNameSet  string
	key         string
	value       string
	values      map[string]string
	models      []*azdext.PromptAiModelRequest
	deployments []*azdext.PromptAiDeploymentRequest
	failAt      int
	failKey     string
}

func (s *recordingProjectEnvironmentServer) GetValues(
	_ context.Context,
	request *azdext.GetEnvironmentRequest,
) (*azdext.KeyValueListResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if request.GetName() != s.envName {
		return nil, fmt.Errorf("unexpected environment %q", request.GetName())
	}
	response := &azdext.KeyValueListResponse{}
	for key, value := range s.values {
		response.KeyValues = append(response.KeyValues, &azdext.KeyValue{Key: key, Value: value})
	}
	return response, nil
}

type recordingProjectPromptServer struct {
	azdext.UnimplementedPromptServiceServer
	environment *recordingProjectEnvironmentServer
}

func (p *recordingProjectPromptServer) PromptAiModel(
	_ context.Context, request *azdext.PromptAiModelRequest,
) (*azdext.PromptAiModelResponse, error) {
	s := p.environment
	s.mu.Lock()
	defer s.mu.Unlock()
	s.models = append(s.models, request)
	if s.failAt == len(s.models) {
		return nil, errors.New("model selection cancelled")
	}
	return &azdext.PromptAiModelResponse{Model: &azdext.AiModel{Name: "env-model"}}, nil
}

func (p *recordingProjectPromptServer) PromptAiDeployment(
	_ context.Context, request *azdext.PromptAiDeploymentRequest,
) (*azdext.PromptAiDeploymentResponse, error) {
	s := p.environment
	s.mu.Lock()
	defer s.mu.Unlock()
	s.deployments = append(s.deployments, request)
	return &azdext.PromptAiDeploymentResponse{Deployment: &azdext.AiModelDeployment{
		ModelName: "env-model", Format: "OpenAI", Version: "env-version",
		Sku: &azdext.AiModelSku{Name: "Standard"}, Capacity: 3,
	}}, nil
}

func (s *recordingProjectEnvironmentServer) GetCurrent(
	context.Context,
	*azdext.EmptyRequest,
) (*azdext.EnvironmentResponse, error) {
	return &azdext.EnvironmentResponse{
		Environment: &azdext.Environment{Name: s.envName},
	}, nil
}

func (s *recordingProjectEnvironmentServer) SetValue(
	_ context.Context,
	request *azdext.SetEnvRequest,
) (*azdext.EmptyResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if request.Key == s.failKey {
		return nil, errors.New("environment write failed")
	}
	s.envNameSet = request.EnvName
	s.key = request.Key
	s.value = request.Value
	if s.values == nil {
		s.values = map[string]string{}
	}
	s.values[request.Key] = request.Value
	return &azdext.EmptyResponse{}, nil
}

func newProjectEnvironmentClient(
	t *testing.T,
	envServer azdext.EnvironmentServiceServer,
) *azdext.AzdClient {
	t.Helper()

	server := grpc.NewServer()
	azdext.RegisterEnvironmentServiceServer(server, envServer)
	if recording, ok := envServer.(*recordingProjectEnvironmentServer); ok {
		azdext.RegisterPromptServiceServer(server, &recordingProjectPromptServer{environment: recording})
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	go func() {
		_ = server.Serve(listener)
	}()
	t.Cleanup(func() {
		server.Stop()
		_ = listener.Close()
	})

	client, err := azdext.NewAzdClient(
		azdext.WithAddress(listener.Addr().String()),
	)
	require.NoError(t, err)
	t.Cleanup(func() {
		client.Close()
	})
	return client
}

func TestProjectLifecycleHandlerPromptFailureDoesNotWrite(t *testing.T) {
	t.Setenv("AZD_NO_PROMPT", "false")
	server := &recordingProjectEnvironmentServer{
		envName: "env-b", failAt: 2,
		values: map[string]string{"AZURE_SUBSCRIPTION_ID": "sub-b", "AZURE_LOCATION": "westus"},
	}
	client := newProjectEnvironmentClient(t, server)
	err := projectLifecycleHandler(t.Context(), client, &azdext.ProjectEventArgs{Project: &azdext.ProjectConfig{
		Services: map[string]*azdext.ServiceConfig{"project": {
			Host: aiProjectHost,
			AdditionalProperties: mustProjectProperties(t, map[string]any{"deployments": []any{
				map[string]any{"name": "chat", "model": map[string]any{"name": "yaml-model"}},
				map[string]any{"name": "embedding", "model": map[string]any{"name": "yaml-embedding"}},
			}}),
		}},
	}})
	require.ErrorContains(t, err, "model selection cancelled")
	assert.Empty(t, server.key)
	assert.NotContains(t, server.values, projectDeploymentsEnvKey)
	assert.Len(t, server.models, 2)
	assert.Len(t, server.deployments, 1)
}

func TestProjectLifecycleHandlerEjectedDoesNotPrompt(t *testing.T) {
	t.Setenv("AZD_NO_PROMPT", "false")
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "azure.yaml"), []byte("name: test\n"), 0600))
	infra := filepath.Join(root, "infra")
	require.NoError(t, os.Mkdir(infra, 0750))
	for name, content := range map[string]string{
		"main.bicep":           "// ejected",
		"main.parameters.json": "{}",
		foundryEjectionMarker:  foundryBicepMarkerVersion,
	} {
		require.NoError(t, os.WriteFile(filepath.Join(infra, name), []byte(content), 0600))
	}
	server := &recordingProjectEnvironmentServer{envName: "dev"}
	client := newProjectEnvironmentClient(t, server)
	require.NoError(t, projectLifecycleHandler(t.Context(), client, &azdext.ProjectEventArgs{
		Project: &azdext.ProjectConfig{Path: root, Services: map[string]*azdext.ServiceConfig{"project": {
			Host: aiProjectHost,
			AdditionalProperties: mustProjectProperties(t, map[string]any{"deployments": []any{
				map[string]any{"name": "chat"},
			}}),
		}}},
	}))
	assert.Empty(t, server.models)
	assert.Contains(t, server.values[projectDeploymentsEnvKey], `\"name\":\"chat\"`)
}

func TestProjectLifecycleHandlerUnmarkedBicepDoesNotPrompt(t *testing.T) {
	t.Setenv("AZD_NO_PROMPT", "false")
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "azure.yaml"), []byte("name: test\n"), 0600))
	infra := filepath.Join(root, "infra")
	require.NoError(t, os.Mkdir(infra, 0750))
	require.NoError(t, os.WriteFile(filepath.Join(infra, "main.bicep"), []byte("// user authored"), 0600))
	server := &recordingProjectEnvironmentServer{envName: "dev"}
	client := newProjectEnvironmentClient(t, server)
	require.NoError(t, projectLifecycleHandler(t.Context(), client, &azdext.ProjectEventArgs{
		Project: &azdext.ProjectConfig{Path: root, Services: map[string]*azdext.ServiceConfig{"project": {
			Host: aiProjectHost,
			AdditionalProperties: mustProjectProperties(t, map[string]any{"deployments": []any{
				map[string]any{"name": "chat"},
			}}),
		}}},
	}))
	assert.Empty(t, server.models)
	assert.Contains(t, server.values[projectDeploymentsEnvKey], `\"name\":\"chat\"`)
}

func TestProjectLifecycleHandlerBicepparamDoesNotPrompt(t *testing.T) {
	t.Setenv("AZD_NO_PROMPT", "false")
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "azure.yaml"), []byte("name: test\n"), 0600))
	infra := filepath.Join(root, "infra")
	require.NoError(t, os.Mkdir(infra, 0750))
	require.NoError(t, os.WriteFile(filepath.Join(infra, "main.bicepparam"), []byte("// user authored"), 0600))
	server := &recordingProjectEnvironmentServer{envName: "dev"}
	client := newProjectEnvironmentClient(t, server)
	require.NoError(t, projectLifecycleHandler(t.Context(), client, &azdext.ProjectEventArgs{
		Project: &azdext.ProjectConfig{Path: root, Services: map[string]*azdext.ServiceConfig{"project": {
			Host: aiProjectHost,
			AdditionalProperties: mustProjectProperties(t, map[string]any{"deployments": []any{
				map[string]any{"name": "chat"},
			}}),
		}}},
	}))
	assert.Empty(t, server.models)
	assert.Contains(t, server.values[projectDeploymentsEnvKey], `\"name\":\"chat\"`)
}

func TestProjectLifecycleHandlerDeploymentPrecedence(t *testing.T) {
	const override = `[{"name":"env-only","model":{"name":"gpt-4.1","format":"OpenAI","version":"1"},` +
		`"sku":{"name":"Standard","capacity":1}}]`
	for _, test := range []struct {
		name, value                     string
		noPrompt, wantPrompt, wantError bool
	}{
		{name: "interactive missing", wantPrompt: true},
		{name: "interactive whitespace", value: " \t", wantPrompt: true},
		{name: "no-prompt defaults", noPrompt: true},
		{name: "existing override", value: override},
		{name: "existing escaped", value: strings.ReplaceAll(override, `"`, `\"`)},
		{name: "intentional empty", value: "[]"},
		{name: "no-prompt override", value: "[]", noPrompt: true},
		{name: "malformed", value: "{", wantError: true},
		{name: "malformed no-prompt", value: "null", noPrompt: true, wantError: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("AZD_NO_PROMPT", strconv.FormatBool(test.noPrompt))
			t.Setenv(projectDeploymentsEnvKey, `[{"name":"process-must-not-win"}]`)
			server := &recordingProjectEnvironmentServer{
				envName: "env-b",
				values: map[string]string{
					projectDeploymentsEnvKey: test.value,
					"AZURE_SUBSCRIPTION_ID":  "sub-b",
					"AZURE_LOCATION":         "westus",
				},
			}

			client := newProjectEnvironmentClient(t, server)
			args := &azdext.ProjectEventArgs{Project: &azdext.ProjectConfig{
				Services: map[string]*azdext.ServiceConfig{"project": {
					Host: aiProjectHost,
					AdditionalProperties: mustProjectProperties(t, map[string]any{
						"deployments": []any{
							map[string]any{
								"name":  "chat",
								"model": map[string]any{"name": "yaml-model", "version": "yaml-version"},
								"sku":   map[string]any{"name": "YamlSku", "capacity": 100},
							},
							map[string]any{"name": "embedding", "model": map[string]any{"name": "yaml-embedding"}},
						},
					}),
				}},
			}}
			err := projectLifecycleHandler(t.Context(), client, args)
			if test.wantError {
				require.ErrorContains(t, err, projectDeploymentsEnvKey)
				assert.Empty(t, server.key)
				assert.Empty(t, server.models)
				assert.Equal(t, test.value, server.values[projectDeploymentsEnvKey])
				return
			}
			require.NoError(t, err)
			if test.wantPrompt {
				require.Len(t, server.models, 2)
				require.Len(t, server.deployments, 2)
				assert.Equal(t, "yaml-model", server.models[0].DefaultValue)
				assert.Equal(t, "yaml-embedding", server.models[1].DefaultValue)
				for _, request := range server.models {
					assert.Equal(t, "sub-b", request.AzureContext.Scope.SubscriptionId)
					assert.Equal(t, "westus", request.AzureContext.Scope.Location)
					assert.Equal(t, []string{"westus"}, request.Filter.Locations)
					assert.Empty(t, request.Filter.Capabilities)
					assert.Equal(t, float64(1), request.Quota.MinRemainingCapacity)
				}
				for _, request := range server.deployments {
					assert.Equal(t, "env-model", request.ModelName)
					assert.Equal(t, []string{"westus"}, request.Options.Locations)
					assert.Empty(t, request.Options.Versions)
					assert.Empty(t, request.Options.Skus)
					assert.Nil(t, request.Options.Capacity)
					assert.Equal(t, float64(1), request.Quota.MinRemainingCapacity)
				}
				resolved, _, err := provisioning.DecodeProjectDeployments(server.values[projectDeploymentsEnvKey])
				require.NoError(t, err)
				require.Len(t, resolved, 2)
				assert.Equal(t, "chat", resolved[0].Name)
				assert.Equal(t, "embedding", resolved[1].Name)
				assert.Equal(t, "env-model", resolved[0].Model.Name)
				assert.Equal(t, "env-version", resolved[0].Model.Version)
				assert.Equal(t, 3, resolved[0].Sku.Capacity)
				assert.Equal(t, "chat", server.values["AZURE_AI_MODEL_DEPLOYMENT_NAME"])
				require.NoError(t, projectLifecycleHandler(t.Context(), client, args))
				assert.Len(t, server.models, 2, "persisted environment must not prompt again")
			} else {
				assert.Empty(t, server.models)
				if test.value == "" {
					assert.Contains(t, server.values[projectDeploymentsEnvKey], `\"name\":\"yaml-model\"`)
					assert.Contains(t, server.values[projectDeploymentsEnvKey], `\"capacity\":100`)
				} else {
					assert.Equal(t, test.value, server.values[projectDeploymentsEnvKey])
				}
			}
		})
	}
}

func TestProjectLifecycleHandlerOnDiskRuntimeDefault(t *testing.T) {
	const key = "AZURE_AI_MODEL_DEPLOYMENT_NAME"
	for _, kind := range []string{"bicep", "bicepparam", "terraform", "terraform-layer"} {
		for _, override := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/override=%v", kind, override), func(t *testing.T) {
				t.Setenv("AZD_NO_PROMPT", "false")
				root := t.TempDir()
				raw := "name: test\n"
				if kind == "terraform-layer" {
					raw += "infra:\n  layers:\n    - name: foundry\n      provider: terraform\n      path: infra\n"
				}
				require.NoError(t, os.WriteFile(filepath.Join(root, "azure.yaml"), []byte(raw), 0600))
				infra := filepath.Join(root, "infra")
				require.NoError(t, os.Mkdir(infra, 0750))
				if strings.HasPrefix(kind, "terraform") {
					require.NoError(t, os.WriteFile(
						filepath.Join(infra, foundryEjectionMarker), []byte(foundryTerraformMarkerVersion), 0600,
					))
				} else {
					require.NoError(t, os.WriteFile(filepath.Join(infra, "main."+kind), []byte("// on disk"), 0600))
				}
				require.NoError(t, os.WriteFile(filepath.Join(infra, "main.parameters.json"),
					[]byte(`{"parameters":{"deployments":{"value":[{"name":"on-disk-chat"}]}}}`), 0600))
				server := &recordingProjectEnvironmentServer{envName: "new-env", values: map[string]string{}}
				if override {
					server.values[projectDeploymentsEnvKey] = `[{"name":"env-chat",` +
						`"model":{"name":"gpt-4.1","format":"OpenAI","version":"1"},` +
						`"sku":{"name":"Standard","capacity":1}}]`
				}
				client := newProjectEnvironmentClient(t, server)
				args := &azdext.ProjectEventArgs{Project: &azdext.ProjectConfig{
					Path: root, Services: map[string]*azdext.ServiceConfig{"project": {
						Host: aiProjectHost,
						AdditionalProperties: mustProjectProperties(t, map[string]any{
							"deployments": []any{map[string]any{
								"name":  "yaml-chat",
								"model": map[string]any{"name": "gpt-4.1", "format": "OpenAI", "version": "1"},
								"sku":   map[string]any{"name": "Standard", "capacity": 1},
							}},
						}),
					}},
				}}
				require.NoError(t, projectLifecycleHandler(t.Context(), client, args))
				assert.NotContains(t, server.values, key)
				assert.Empty(t, server.models)
				server.values[key] = "on-disk-chat"
				require.NoError(t, projectLifecycleHandler(t.Context(), client, args))
				assert.Equal(t, "on-disk-chat", server.values[key])
			})
		}
	}
}

func TestProjectLifecycleHandlerSeedsRuntimeModelName(t *testing.T) {
	const key = "AZURE_AI_MODEL_DEPLOYMENT_NAME"
	const override = `[{"name":"env-chat","model":{"name":"gpt-4.1","format":"OpenAI","version":"1"},` +
		`"sku":{"name":"Standard","capacity":1}}]`
	for _, test := range []struct {
		name, override, current, want string
		failKey                       string
	}{
		{name: "fresh environment", want: "yaml-chat"},
		{name: "existing override", override: override, want: "env-chat"},
		{name: "escaped override", override: strings.ReplaceAll(override, `"`, `\"`), want: "env-chat"},
		{name: "blank default", override: override, current: " \t", want: "env-chat"},
		{name: "explicit default", current: "custom-chat", want: "custom-chat"},
		{name: "reused default", override: override, current: "reused-chat", want: "reused-chat"},
		{name: "empty override", override: "[]"},
		{name: "empty override preserves reuse", override: "[]", current: "reused-chat", want: "reused-chat"},
		{name: "list write failure", failKey: projectDeploymentsEnvKey},
		{name: "default write failure", failKey: key},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("AZD_NO_PROMPT", "true")
			t.Setenv(key, "process-must-not-win")
			server := &recordingProjectEnvironmentServer{
				envName: "fresh-env", failKey: test.failKey,
				values: map[string]string{projectDeploymentsEnvKey: test.override, key: test.current},
			}
			client := newProjectEnvironmentClient(t, server)
			args := &azdext.ProjectEventArgs{Project: &azdext.ProjectConfig{
				Services: map[string]*azdext.ServiceConfig{"project": {
					Host: aiProjectHost,
					AdditionalProperties: mustProjectProperties(t, map[string]any{
						"deployments": []any{map[string]any{
							"name":  "yaml-chat",
							"model": map[string]any{"name": "gpt-4.1", "format": "OpenAI", "version": "1"},
							"sku":   map[string]any{"name": "Standard", "capacity": 1},
						}},
					}),
				}},
			}}
			err := projectLifecycleHandler(t.Context(), client, args)
			if test.failKey != "" {
				require.ErrorContains(t, err, test.failKey)
				assert.Equal(t, test.current, server.values[key])
				if test.failKey == key {
					server.failKey = ""
					require.NoError(t, projectLifecycleHandler(t.Context(), client, args))
					assert.Equal(t, "yaml-chat", server.values[key])
				}
				return
			}
			require.NoError(t, err)
			assert.Equal(t, test.want, server.values[key])
			if test.want != test.current {
				assert.Equal(t, "fresh-env", server.envNameSet)
			}
			if test.override != "" {
				assert.Equal(t, test.override, server.values[projectDeploymentsEnvKey])
			}
			require.NoError(t, projectLifecycleHandler(t.Context(), client, args))
			assert.Equal(t, test.want, server.values[key])
			assert.Empty(t, server.models)
		})
	}
}
