// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"azureaiagent/internal/featureflags"
	"azureaiagent/internal/synthesis"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
)

type acrFeatureEnvironment struct {
	stubInitializeEnvServer
	values map[string]string
	config map[string][]byte
	writes int
}

func (s *acrFeatureEnvironment) GetValues(
	context.Context, *azdext.GetEnvironmentRequest,
) (*azdext.KeyValueListResponse, error) {
	result := &azdext.KeyValueListResponse{}
	for key, value := range s.values {
		result.KeyValues = append(result.KeyValues, &azdext.KeyValue{Key: key, Value: value})
	}
	return result, nil
}

func (s *acrFeatureEnvironment) GetConfig(
	_ context.Context, request *azdext.GetConfigRequest,
) (*azdext.GetConfigResponse, error) {
	value, found := s.config[request.GetPath()]
	return &azdext.GetConfigResponse{Value: value, Found: found}, nil
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
				require.Equal(t, "feature_not_enabled", local.Code)
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
