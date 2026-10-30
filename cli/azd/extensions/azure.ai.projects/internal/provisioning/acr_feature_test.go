// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package provisioning

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"os"
	"path/filepath"
	"testing"

	"azure.ai.projects/internal/synthesis"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/bicep"
	"github.com/stretchr/testify/require"
)

func requireReuseDisabled(t *testing.T, err error) {
	t.Helper()
	local, ok := errors.AsType[*azdext.LocalError](err)
	require.True(t, ok, "%v", err)
	require.Equal(t, "feature_not_enabled", local.Code)
	require.Equal(t, azdext.LocalErrorCategoryCompatibility, local.Category)
	require.Contains(t, local.Message, synthesis.ACRReuseEnvVar+"=true")
}

func TestACRReuseDisabledProviderEntrypoints(t *testing.T) {
	operations := map[string]func(*FoundryProvisioningProvider) error{
		"state": func(p *FoundryProvisioningProvider) error {
			_, err := p.State(t.Context(), &azdext.ProvisioningStateOptions{})
			return err
		},
		"deploy": func(p *FoundryProvisioningProvider) error {
			_, err := p.Deploy(t.Context(), func(string) {})
			return err
		},
		"preview": func(p *FoundryProvisioningProvider) error {
			_, err := p.Preview(t.Context(), func(string) {})
			return err
		},
		"down": func(p *FoundryProvisioningProvider) error {
			_, err := p.Destroy(t.Context(), &azdext.ProvisioningDestroyOptions{Force: true}, func(string) {})
			return err
		},
		"parameters": func(p *FoundryProvisioningProvider) error {
			_, err := p.Parameters(t.Context())
			return err
		},
		"planned outputs": func(p *FoundryProvisioningProvider) error {
			_, err := p.PlannedOutputs(t.Context())
			return err
		},
		"recovery": func(p *FoundryProvisioningProvider) error {
			return p.recoverDeployedACRBinding(t.Context())
		},
		"partial recovery": func(p *FoundryProvisioningProvider) error {
			return p.recoverPartialACRBinding(t.Context(), "")
		},
		"cleanup": func(p *FoundryProvisioningProvider) error {
			return p.cleanupACRAccess(t.Context())
		},
	}
	for _, state := range []string{"selector", "binding without aliases", "pending without aliases"} {
		for name, operation := range operations {
			t.Run(state+"/"+name, func(t *testing.T) {
				p, env := acrTestProvider(t)
				switch state {
				case "selector":
					p.synthesisEnv = map[string]string{"AZURE_CONTAINER_REGISTRY_RESOURCE_ID": testACRID}
				case "binding without aliases":
					p.acrBinding = acrTestBinding()
				case "pending without aliases":
					p.acrPending = acrTestPending()
				}
				env.config = map[string][]byte{"sentinel": []byte(`{"keep":true}`)}
				before := maps.Clone(env.config)
				azureCalls := 0
				p.acrRequest = func(context.Context, string, string, string, any) error {
					azureCalls++
					return errors.New("unexpected Azure request")
				}
				t.Setenv(synthesis.ACRReuseEnvVar, "")
				requireReuseDisabled(t, operation(p))
				require.Zero(t, azureCalls)
				require.Empty(t, env.set)
				require.Equal(t, before, env.config)
			})
		}
	}
}

func TestACRReuseDisabledInitializeBeforeWrites(t *testing.T) {
	for _, state := range []string{"selector", "canonical", "pending", "ejected parameters", "ejected bicepparam"} {
		for _, layer := range []bool{false, true} {
			t.Run(state+"/layer="+map[bool]string{false: "false", true: "true"}[layer], func(t *testing.T) {
				p, env := acrTestProvider(t)
				t.Setenv(synthesis.ACRReuseEnvVar, "")
				root := t.TempDir()
				raw := "services:\n  project:\n    host: azure.ai.project\n" +
					"  agent:\n    host: azure.ai.agent\n    kind: hosted\n"
				options := &azdext.ProvisioningOptions{Provider: FoundryProviderName}
				if layer {
					raw += "infra:\n  layers:\n    - name: foundry\n      provider: microsoft.foundry\n" +
						"      path: infra/foundry\n"
					options.Name, options.Path = "foundry", filepath.Join("infra", "foundry")
				}
				path := filepath.Join(root, "azure.yaml")
				require.NoError(t, os.WriteFile(path, []byte(raw), 0o600))
				env.get = map[string]string{
					"AZURE_SUBSCRIPTION_ID": "sub", "AZURE_RESOURCE_GROUP": "foundry",
					"AZURE_FOUNDRY_RESOURCE_GROUP": "foundry",
				}
				env.config = map[string][]byte{}
				switch state {
				case "selector":
					env.get["AZURE_CONTAINER_REGISTRY_RESOURCE_ID"] = testACRID
				case "canonical":
					data, err := json.Marshal(acrTestBinding())
					require.NoError(t, err)
					env.config[synthesis.ACRBindingConfigKey] = data
				case "pending":
					data, err := json.Marshal(acrTestPending())
					require.NoError(t, err)
					env.config[acrPendingConfigKey] = data
				default:
					infraPath := options.Path
					if infraPath == "" {
						infraPath = onDiskInfraDir
					}
					infraPath = filepath.Join(root, infraPath)
					require.NoError(t, os.MkdirAll(infraPath, 0o750))
					template := `{"parameters":{"existingAcrResourceId":{"type":"string"},` +
						`"includeAcr":{"type":"bool","defaultValue":true}},"resources":[]}`
					params := minimalARMParametersFile(t, map[string]any{"existingAcrResourceId": testACRID})
					compiler := &stubCompiler{buildResult: bicep.BuildResult{Compiled: template}}
					if state == "ejected bicepparam" {
						compiled, err := json.Marshal(map[string]string{"templateJson": template, "parametersJson": params})
						require.NoError(t, err)
						compiler.buildParamResult.Compiled = string(compiled)
						require.NoError(t, os.WriteFile(filepath.Join(infraPath, "main.bicepparam"), []byte("using './main.bicep'"), 0o600))
					} else {
						require.NoError(t, os.WriteFile(filepath.Join(infraPath, "main.bicep"), []byte("// generated"), 0o600))
						require.NoError(t, os.WriteFile(filepath.Join(infraPath, "main.parameters.json"), []byte(params), 0o600))
					}
					p.bicepCliInstance = compiler
					p.azdClient = newResolveEnvTestClient(t, env, &resolveEnvStubPromptServer{location: "eastus2"})
				}
				beforeValues, beforeConfig := maps.Clone(env.get), maps.Clone(env.config)
				requireReuseDisabled(t, p.Initialize(t.Context(), root, options))
				require.Empty(t, env.set, "prompted values must not be persisted before the Bicep gate")
				require.Equal(t, beforeValues, env.get)
				require.Equal(t, beforeConfig, env.config)
				// #nosec G304 -- isolated test fixture.
				after, err := os.ReadFile(path)
				require.NoError(t, err)
				require.Equal(t, raw, string(after))
			})
		}
	}
}
