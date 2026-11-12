// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"os"
	"path/filepath"
	"testing"

	"azure.ai.projects/synthesis"
	"azureaiagent/internal/featureflags"
	"azureaiagent/internal/testutil"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

type acrFeatureEnvironment struct {
	stubInitializeEnvServer
	values        map[string]string
	config        map[string][]byte
	writes        int
	selected      string
	currentErr    error
	namedErr      error
	valuesErr     error
	defaultConfig map[string][]byte
}

func (s *acrFeatureEnvironment) GetCurrent(
	ctx context.Context, request *azdext.EmptyRequest,
) (*azdext.EnvironmentResponse, error) {
	if s.currentErr != nil {
		return nil, s.currentErr
	}
	return s.stubInitializeEnvServer.GetCurrent(ctx, request)
}

func (s *acrFeatureEnvironment) Get(
	_ context.Context, request *azdext.GetEnvironmentRequest,
) (*azdext.EnvironmentResponse, error) {
	if s.namedErr != nil {
		return nil, s.namedErr
	}
	if request.GetName() != s.selected {
		return nil, status.Error(codes.NotFound, "selected environment not found")
	}
	return &azdext.EnvironmentResponse{Environment: &azdext.Environment{Name: s.selected}}, nil
}

func (s *acrFeatureEnvironment) GetValues(
	_ context.Context, request *azdext.GetEnvironmentRequest,
) (*azdext.KeyValueListResponse, error) {
	if s.valuesErr != nil {
		return nil, s.valuesErr
	}
	if s.selected != "" && request.GetName() == "test-env" {
		return &azdext.KeyValueListResponse{}, nil
	}
	if s.selected != "" && request.GetName() != s.selected {
		return nil, status.Error(codes.FailedPrecondition, "read the wrong environment")
	}
	result := &azdext.KeyValueListResponse{}
	for key, value := range s.values {
		result.KeyValues = append(result.KeyValues, &azdext.KeyValue{Key: key, Value: value})
	}
	return result, nil
}

func (s *acrFeatureEnvironment) GetConfig(
	_ context.Context, request *azdext.GetConfigRequest,
) (*azdext.GetConfigResponse, error) {
	if s.selected != "" && request.GetEnvName() == "test-env" {
		value, found := s.defaultConfig[request.GetPath()]
		return &azdext.GetConfigResponse{Value: value, Found: found}, nil
	}
	if s.selected != "" && request.GetEnvName() != s.selected {
		return nil, status.Error(codes.FailedPrecondition, "read the wrong lifecycle state")
	}
	value, found := s.config[request.GetPath()]
	return &azdext.GetConfigResponse{Value: value, Found: found}, nil
}

func TestACRGuardWithoutCurrentEnvironment(t *testing.T) {
	t.Setenv(synthesis.ACRReuseEnvVar, "")
	for _, test := range []struct {
		name        string
		selection   string
		environment acrFeatureEnvironment
		code        codes.Code
	}{
		{name: "missing default", environment: acrFeatureEnvironment{
			currentErr: status.Error(codes.Unknown, "default environment not found"),
		}},
		{name: "unavailable", code: codes.Unavailable, environment: acrFeatureEnvironment{
			currentErr: status.Error(codes.Unavailable, "host unavailable"),
		}},
		{name: "permission denied", code: codes.PermissionDenied, environment: acrFeatureEnvironment{
			currentErr: status.Error(codes.PermissionDenied, "access denied"),
		}},
		{name: "lookalike message", code: codes.Unknown, environment: acrFeatureEnvironment{
			currentErr: status.Error(codes.Unknown, "read failed: default environment not found"),
		}},
		{name: "different code", code: codes.NotFound, environment: acrFeatureEnvironment{
			currentErr: status.Error(codes.NotFound, "default environment not found"),
		}},
		{name: "named environment", selection: "selected", code: codes.Unknown, environment: acrFeatureEnvironment{
			namedErr: status.Error(codes.Unknown, "default environment not found"),
		}},
		{name: "values read", code: codes.Unknown, environment: acrFeatureEnvironment{
			valuesErr: status.Error(codes.Unknown, "default environment not found"),
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("AZD_ENVIRONMENT", test.selection)
			client := newEnvTestClient(t, &test.environment)
			for _, reuse := range []bool{false, true} {
				t.Run(map[bool]string{false: "clean", true: "on-disk reuse"}[reuse], func(t *testing.T) {
					root := t.TempDir()
					id := ""
					if reuse {
						id = "/subscriptions/sub/resourceGroups/shared/providers/Microsoft.ContainerRegistry/registries/registry"
					}
					testutil.ACRParameters(t, root, false, "json", id)
					project := &azdext.ProjectConfig{Path: root}
					before := testutil.Snapshot(t, root)
					for _, stateOnly := range []bool{false, true} {
						check := synthesis.RequireACRReuseForProject
						if stateOnly {
							check = synthesis.RequireACRReuseForProjectState
						}
						err := check(t.Context(), client, project)
						if test.code != codes.OK {
							require.Equal(t, test.code, status.Code(err), "%v", err)
						} else if reuse && !stateOnly {
							local, ok := errors.AsType[*azdext.LocalError](err)
							require.True(t, ok, "%v", err)
							require.Equal(t, "feature_not_enabled", local.Code)
						} else {
							require.NoError(t, err)
						}
					}
					require.Zero(t, test.environment.writes)
					require.Equal(t, before, testutil.Snapshot(t, root))
				})
			}
		})
	}
}

func TestACRPackageWithoutCurrentEnvironment(t *testing.T) {
	t.Setenv(synthesis.ACRReuseEnvVar, "")
	t.Setenv("AZD_ENVIRONMENT", "")
	env := &acrFeatureEnvironment{currentErr: status.Error(codes.Unknown, "default environment not found")}
	service := &azdext.ServiceConfig{
		Name: "agent", Host: foundryAgentHost,
		AdditionalProperties: mustStruct(t, map[string]any{
			"kind": "prompt", "name": "agent", "model": "gpt-4.1", "instructions": "Help.",
		}),
	}
	p := &AgentServiceTargetProvider{azdClient: newEnvTestClient(t, env), projectPath: t.TempDir()}
	_, err := p.Package(t.Context(), service, &azdext.ServiceContext{}, func(string) {})
	require.NoError(t, err)
	require.Zero(t, env.writes)
}

func TestACRRuntimeSelectedEnvironment(t *testing.T) {
	const registryID = "/subscriptions/sub/resourceGroups/shared/providers/Microsoft.ContainerRegistry/registries/registry"
	binding, err := json.Marshal(synthesis.ACRBinding{
		Version: 1, Mode: "reuse-connect", RegistryResourceID: registryID,
		ProjectResourceID: "/subscriptions/sub/resourceGroups/foundry/" +
			"providers/Microsoft.CognitiveServices/accounts/account/projects/project",
		PrincipalID: "principal",
	})
	require.NoError(t, err)
	for _, state := range []string{"clean", "pending", "binding", "selector"} {
		for _, noDefault := range []bool{false, true} {
			t.Run(state+"/"+map[bool]string{false: "other default", true: "no default"}[noDefault], func(t *testing.T) {
				t.Setenv(synthesis.ACRReuseEnvVar, "")
				t.Setenv("AZD_ENVIRONMENT", "selected")
				env := &acrFeatureEnvironment{
					selected: "selected", config: map[string][]byte{},
				}
				if noDefault {
					env.currentErr = status.Error(codes.Unknown, "default environment not found")
				}
				switch state {
				case "pending":
					env.config[synthesis.ACRPendingConfigKey] = []byte(`{"version":1}`)
				case "binding":
					env.config[synthesis.ACRBindingConfigKey] = binding
				case "selector":
					env.values = map[string]string{"AZURE_CONTAINER_REGISTRY_RESOURCE_ID": registryID}
				case "clean":
					env.defaultConfig = map[string][]byte{
						synthesis.ACRBindingConfigKey: binding,
					}
				}
				service := &azdext.ServiceConfig{
					Name: "agent", Host: foundryAgentHost,
					AdditionalProperties: mustStruct(t, map[string]any{
						"kind": "prompt", "name": "agent", "model": "gpt-4.1", "instructions": "Help.",
					}),
				}
				p := &AgentServiceTargetProvider{
					azdClient: newEnvTestClient(t, env), projectPath: t.TempDir(),
				}
				_, err := p.Package(t.Context(), service, &azdext.ServiceContext{}, func(string) {})
				if state != "clean" {
					local, ok := errors.AsType[*azdext.LocalError](err)
					require.True(t, ok, "%v", err)
					require.Equal(t, "feature_not_enabled", local.Code)
				} else {
					require.NoError(t, err)
					require.NoError(t, p.ensureEnv(t.Context()))
					require.Equal(t, "selected", p.env.GetName())
				}
				require.Zero(t, env.writes)
			})
		}
	}
}

func (s *acrFeatureEnvironment) SetValue(
	context.Context, *azdext.SetEnvRequest,
) (*azdext.EmptyResponse, error) {
	s.writes++
	return nil, errors.New("unexpected environment mutation")
}

func TestACRReuseDisabledAgentRuntimeEntrypoints(t *testing.T) {
	const registryID = "/subscriptions/sub/resourceGroups/shared/providers/Microsoft.ContainerRegistry/registries/registry"
	for _, state := range []string{"selector", "canonical", "pending"} {
		for _, operation := range []string{"package", "publish", "deploy"} {
			t.Run(state+"/"+operation, func(t *testing.T) {
				t.Setenv(synthesis.ACRReuseEnvVar, "")
				t.Setenv(featureflags.Voice.EnvVar, "true")
				root := t.TempDir()
				env := &acrFeatureEnvironment{values: map[string]string{"AZURE_SUBSCRIPTION_ID": "sub"},
					config: map[string][]byte{}}
				switch state {
				case "selector":
					env.values["AZURE_CONTAINER_REGISTRY_RESOURCE_ID"] = registryID
				case "canonical":
					binding, err := json.Marshal(synthesis.ACRBinding{
						Version: 1, Mode: "reuse-connect", RegistryResourceID: registryID,
						ProjectResourceID: "/subscriptions/sub/resourceGroups/foundry/" +
							"providers/Microsoft.CognitiveServices/accounts/account/projects/project",
						PrincipalID: "principal",
					})
					require.NoError(t, err)
					env.config[synthesis.ACRBindingConfigKey] = binding
				case "pending":
					env.config[synthesis.ACRPendingConfigKey] = []byte(`{"version":1}`)
				}

				path := filepath.Join(root, "azure.yaml")
				raw := []byte("services:\n  project:\n    host: azure.ai.project\n")
				require.NoError(t, os.WriteFile(path, raw, 0o600))
				service := hostedTestService(t, "agent", "")
				p := &AgentServiceTargetProvider{azdClient: newEnvTestClient(t, env), projectPath: root}
				serviceContext := &azdext.ServiceContext{}
				before := proto.Clone(serviceContext)
				var err error
				switch operation {
				case "package":
					_, err = p.Package(t.Context(), service, serviceContext, func(string) {})
				case "publish":
					_, err = p.Publish(t.Context(), service, serviceContext, nil, nil, func(string) {})
				case "deploy":
					_, err = p.Deploy(t.Context(), service, serviceContext, nil, func(string) {})
				}
				local, ok := errors.AsType[*azdext.LocalError](err)
				require.True(t, ok, "%v", err)
				require.Equal(t, "feature_not_enabled", local.Code, "%s", local.Message)
				require.Contains(t, local.Message, synthesis.ACRReuseEnvVar+"=true")
				require.Zero(t, env.writes)
				require.True(t, proto.Equal(before, serviceContext))
				after, err := os.ReadFile(path)
				require.NoError(t, err)
				require.Equal(t, raw, after)
				require.NoDirExists(t, filepath.Join(root, ".azure"))
			})
		}
	}
}

func TestACRParameterOnlyAgentRuntimeEntrypoints(t *testing.T) {
	const registryID = "/subscriptions/sub/resourceGroups/shared/providers/Microsoft.ContainerRegistry/registries/registry"
	for _, layer := range []bool{false, true} {
		for _, format := range []string{"json", "bicepparam"} {
			for _, operation := range []string{"package", "publish", "deploy"} {
				name := map[bool]string{false: "root", true: "layer"}[layer] + "/" + format + "/" + operation
				t.Run(name, func(t *testing.T) {
					t.Setenv(synthesis.ACRReuseEnvVar, "")
					root := t.TempDir()
					testutil.ACRParameters(t, root, layer, format, registryID)
					env := &acrFeatureEnvironment{
						values: map[string]string{"AZURE_SUBSCRIPTION_ID": "sub"}, config: map[string][]byte{},
					}
					beforeValues, beforeConfig := maps.Clone(env.values), maps.Clone(env.config)
					beforeFiles := testutil.Snapshot(t, root)
					configRoot := os.Getenv("AZD_CONFIG_DIR")
					beforeUserConfig := testutil.Snapshot(t, configRoot)
					service := hostedTestService(t, "agent", "")
					p := &AgentServiceTargetProvider{azdClient: newEnvTestClient(t, env), projectPath: root}
					serviceContext := &azdext.ServiceContext{}
					before := proto.Clone(serviceContext)
					var err error
					switch operation {
					case "package":
						_, err = p.Package(t.Context(), service, serviceContext, func(string) {})
					case "publish":
						_, err = p.Publish(t.Context(), service, serviceContext, nil, nil, func(string) {})
					case "deploy":
						_, err = p.Deploy(t.Context(), service, serviceContext, nil, func(string) {})
					}
					local, ok := errors.AsType[*azdext.LocalError](err)
					require.True(t, ok, "%v", err)
					require.Equal(t, "feature_not_enabled", local.Code)
					require.Equal(t, azdext.LocalErrorCategoryCompatibility, local.Category)
					require.Contains(t, local.Message, synthesis.ACRReuseEnvVar+"=true")
					require.Zero(t, env.writes)
					require.Equal(t, beforeValues, env.values)
					require.Equal(t, beforeConfig, env.config)
					require.True(t, proto.Equal(before, serviceContext))
					require.Equal(t, beforeFiles, testutil.Snapshot(t, root))
					require.Equal(t, beforeUserConfig, testutil.Snapshot(t, configRoot))
				})
			}
		}
	}

}

func TestACRParameterOnlyAgentRuntimePositivePaths(t *testing.T) {
	const registryID = "/subscriptions/sub/resourceGroups/shared/providers/Microsoft.ContainerRegistry/registries/registry"
	for _, policy := range []struct {
		name, id, flag string
	}{{"enabled reuse", registryID, "true"}, {"disabled create", "", ""}} {
		for _, layer := range []bool{false, true} {
			for _, format := range []string{"json", "bicepparam"} {
				for _, operation := range []string{"package", "publish", "deploy"} {
					name := policy.name + "/" + map[bool]string{false: "root", true: "layer"}[layer] +
						"/" + format + "/" + operation
					t.Run(name, func(t *testing.T) {
						t.Setenv(synthesis.ACRReuseEnvVar, policy.flag)
						root := t.TempDir()
						testutil.ACRParameters(t, root, layer, format, policy.id)
						env := &acrFeatureEnvironment{
							values: map[string]string{"AZURE_SUBSCRIPTION_ID": "sub"}, config: map[string][]byte{},
						}
						service := &azdext.ServiceConfig{
							Name: "agent", Host: foundryAgentHost,
							AdditionalProperties: mustStruct(t, map[string]any{
								"kind": "prompt", "name": "agent", "model": "gpt-4.1", "instructions": "Help.",
							}),
						}
						p := &AgentServiceTargetProvider{azdClient: newEnvTestClient(t, env), projectPath: root}
						var err error
						switch operation {
						case "package":
							_, err = p.Package(t.Context(), service, &azdext.ServiceContext{}, func(string) {})
						case "publish":
							_, err = p.Publish(t.Context(), service, &azdext.ServiceContext{}, nil, nil, func(string) {})
						case "deploy":
							_, err = p.Deploy(t.Context(), service, &azdext.ServiceContext{}, nil, func(string) {})
						}
						if operation == "deploy" {
							local, ok := errors.AsType[*azdext.LocalError](err)
							require.True(t, ok, "%v", err)
							require.Equal(t, "project_not_found", local.Code, "the next injected dependency must be reached")
						} else {
							require.NoError(t, err)
						}
						require.Zero(t, env.writes)
					})
				}
			}
		}
	}
}

func TestACRReuseDisabledWithoutAzdClient(t *testing.T) {
	t.Setenv(synthesis.ACRReuseEnvVar, "")
	root := t.TempDir()
	testutil.ACRParameters(t, root, false, "json",
		"/subscriptions/sub/resourceGroups/shared/providers/Microsoft.ContainerRegistry/registries/registry")
	service := hostedTestService(t, "agent", "")
	p := &AgentServiceTargetProvider{projectPath: root, serviceConfig: service}
	err := p.prepareGatedOperation(t.Context(), service)
	local, ok := errors.AsType[*azdext.LocalError](err)
	require.True(t, ok, "%v", err)
	require.Equal(t, "feature_not_enabled", local.Code, "%s", local.Message)
	require.Contains(t, local.Message, synthesis.ACRReuseEnvVar+"=true")
}
