// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package provisioning

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"azure.ai.projects/internal/synthesis"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/bicep"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func requireConnectionMigrationError(t *testing.T, err error) {
	t.Helper()
	require.Error(t, err)
	local, ok := errors.AsType[*azdext.LocalError](err)
	require.True(t, ok)
	assert.Equal(t, azdext.LocalErrorCategoryValidation, local.Category)
	assert.Contains(t, local.Message, "removed generic Connection provisioning contract")
	assert.Contains(t, local.Suggestion, "remove")
	assert.Contains(t, local.Suggestion, "connections/connectionCredentials")
	assert.Contains(t, local.Suggestion, "azure.ai.connection")
	assert.Contains(t, local.Suggestion, "azd deploy")
	assert.Contains(t, local.Suggestion, "keep the system ACR connection")
	assert.NotContains(t, err.Error()+local.Suggestion, "synthetic-private-value")
}

func TestLoadOnDiskTemplateRejectsLegacyConnectionContract(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"connections", "connectionCredentials", "ConnectionCredentials"} {
		for _, location := range []string{"template", "parameters"} {
			for _, mode := range []templateMode{templateModeBicep, templateModeBicepParam} {
				t.Run(name+"/"+location+"/"+mode.String(), func(t *testing.T) {
					root := t.TempDir()
					infraDir := filepath.Join(root, onDiskInfraDir)
					require.NoError(t, os.MkdirAll(infraDir, 0o750))
					template := minimalARMTemplate()
					params := minimalARMParametersFile(t, nil)
					if location == "template" {
						template = `{"parameters":{"` + name + `":{"type":"secureObject"}},"resources":[]}`
					} else {
						params = minimalARMParametersFile(t, map[string]any{
							name: map[string]any{"key": "synthetic-private-value", "target": "${ENDPOINT}"},
						})
					}
					bicepPath := filepath.Join(infraDir, onDiskBicepFile)
					paramsPath := filepath.Join(infraDir, onDiskParamsFile)
					bicepBody := "// user-owned Bicep"
					require.NoError(t, os.WriteFile(bicepPath, []byte(bicepBody), 0o600))
					require.NoError(t, os.WriteFile(paramsPath, []byte(params), 0o600))
					compiler := &stubCompiler{buildResult: bicep.BuildResult{Compiled: template}}
					if mode == templateModeBicepParam {
						bicepPath = filepath.Join(infraDir, onDiskBicepParamFile)
						bicepBody = "using './main.bicep'\n// user-owned parameters"
						require.NoError(t, os.WriteFile(bicepPath, []byte(bicepBody), 0o600))
						envelope, err := json.Marshal(map[string]string{
							"templateJson": template, "parametersJson": params,
						})
						require.NoError(t, err)
						compiler.buildParamResult = bicep.BuildResult{Compiled: string(envelope)}
					}

					source, err := loadOnDiskTemplate(t.Context(), root, compiler,
						map[string]string{"ENDPOINT": "synthetic-private-value"})
					requireConnectionMigrationError(t, err)
					assert.Nil(t, source)
					// The breaking migration is validation only, not template rewriting.
					//nolint:gosec // Test-owned file under t.TempDir.
					body, err := os.ReadFile(bicepPath)
					require.NoError(t, err)
					assert.Equal(t, bicepBody, string(body))
					//nolint:gosec // Test-owned file under t.TempDir.
					body, err = os.ReadFile(paramsPath)
					require.NoError(t, err)
					assert.Equal(t, params, string(body))
					entries, err := os.ReadDir(infraDir)
					require.NoError(t, err)
					wantFiles := 2
					if mode == templateModeBicepParam {
						wantFiles = 3
					}
					assert.Len(t, entries, wantFiles, "validation must not create artifacts")
				})
			}
		}
	}
}

func TestValidateProjectTemplateRejectsGenericConnections(t *testing.T) {
	t.Parallel()
	const connection = `{"type":"Microsoft.CognitiveServices/accounts/projects/connections",
		"name":"synthetic-private-value","properties":{"category":"RemoteTool","authType":"None"}}`
	for _, tt := range []struct {
		name, template string
	}{
		{"direct resource", `{"resources":[` + connection + `]}`},
		{"symbolic resource", `{"languageVersion":"2.0","resources":{"renamed":` + connection + `}}`},
		{"account connection", `{"resources":[{"type":"Microsoft.CognitiveServices/accounts/connections"}]}`},
		{"renamed nested module", `{"resources":[{"type":"Microsoft.Resources/deployments","name":"custom",
			"properties":{"template":{"resources":[` + connection + `]}}}]}`},
		{"relative child resource", `{"resources":[{"type":"Microsoft.CognitiveServices/accounts","resources":[
			{"type":"projects","resources":[{"type":"connections","properties":{"category":"CognitiveSearch"}}]}]}]}`},
		{"expression properties", `{"resources":[{"type":"Microsoft.CognitiveServices/accounts/projects/connections",
			"properties":"[variables('customPayload')]"}]}`},
		{"nested parameter declaration", `{"resources":[{"type":"Microsoft.Resources/deployments","properties":{
			"template":{"parameters":{"connections":{"type":"array","defaultValue":[]}}}}}]}`},
		{"linked module parameter", `{"resources":[{"type":"Microsoft.Resources/deployments","properties":{
			"parameters":{"connectionCredentials":{"value":{}}},"templateLink":{"uri":"https://example.com"}}}]}`},
		{"disabled resource", `{"resources":[{"type":"Microsoft.CognitiveServices/accounts/projects/connections",
			"condition":false}]}`},
		{"non-system registry", `{"resources":[{"type":"Microsoft.CognitiveServices/accounts/projects/connections",
			"properties":{"category":"ContainerRegistry","authType":"ApiKey"}}]}`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			_, err := unmarshalARMTemplate(tt.template, "project.bicep")
			requireConnectionMigrationError(t, err)
		})
	}
}

func TestValidateProjectTemplatePreservesSystemAcr(t *testing.T) {
	t.Parallel()
	for _, load := range []struct {
		name string
		load func() ([]byte, error)
	}{
		{"greenfield", synthesis.ARMTemplate},
		{"brownfield", synthesis.ExistingProjectARMTemplate},
	} {
		t.Run(load.name, func(t *testing.T) {
			raw, err := load.load()
			require.NoError(t, err)
			// Check the real system registry resource, not a permissive fake.
			require.True(t, bytes.Contains(raw, []byte(`"ContainerRegistry"`)), "system ACR must be present")
			require.True(t, bytes.Contains(raw, []byte(`"ManagedIdentity"`)), "system ACR must use managed identity")
			var template map[string]any
			require.NoError(t, json.Unmarshal(raw, &template))
			before, err := json.Marshal(template)
			require.NoError(t, err)
			require.NoError(t, validateProjectTemplate(template, "project.bicep"))
			after, err := json.Marshal(template)
			require.NoError(t, err)
			assert.True(t, bytes.Equal(before, after), "validation must not modify the template")
		})
	}
}

func TestLoadOnDiskTemplateGenericSubstitutionUnaffected(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	infraDir := filepath.Join(root, onDiskInfraDir)
	require.NoError(t, os.MkdirAll(infraDir, 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(infraDir, onDiskBicepFile), []byte("// project"), 0o600))
	params := minimalARMParametersFile(t, map[string]any{
		"custom": map[string]any{
			"connections":           []any{map[string]any{"target": "${ENDPOINT}", "path": "${PATH_VALUE}"}},
			"connectionCredentials": map[string]any{"literal": "${VALUE}"},
		},
		"defaulted": "${MISSING=fallback}",
		"dropped":   "${MISSING}",
	})
	require.NoError(t, os.WriteFile(filepath.Join(infraDir, onDiskParamsFile), []byte(params), 0o600))
	template := `{"resources":[{"type":"Custom.Provider/widgets","properties":{"connections":[]}}]}`
	compiler := &stubCompiler{buildResult: bicep.BuildResult{Compiled: template}}
	env := map[string]string{"ENDPOINT": "https://example.com", "PATH_VALUE": `C:\quoted "path"`, "VALUE": "value"}
	source, err := loadOnDiskTemplate(t.Context(), root, compiler, env)
	require.NoError(t, err)
	assert.Equal(t, map[string]any{"value": map[string]any{
		"connections":           []any{map[string]any{"target": env["ENDPOINT"], "path": env["PATH_VALUE"]}},
		"connectionCredentials": map[string]any{"literal": "value"},
	}}, source.parameters["custom"])
	assert.Equal(t, map[string]any{"value": "fallback"}, source.parameters["defaulted"])
	assert.NotContains(t, source.parameters, "dropped")
}

func TestLegacyConnectionTemplateFailsBeforeAzure(t *testing.T) {
	t.Parallel()
	for _, operation := range []string{"deploy", "preview"} {
		t.Run(operation, func(t *testing.T) {
			root := t.TempDir()
			infraDir := filepath.Join(root, onDiskInfraDir)
			require.NoError(t, os.MkdirAll(infraDir, 0o750))
			require.NoError(t, os.WriteFile(filepath.Join(infraDir, onDiskBicepFile), []byte("// legacy"), 0o600))
			provider := &FoundryProvisioningProvider{
				projectPath: root,
				// No Azure client or credential: validation must precede Azure access.
				bicepCliInstance: &stubCompiler{buildResult: bicep.BuildResult{
					Compiled: `{"resources":[{"type":"Microsoft.CognitiveServices/accounts/projects/connections",
						"name":"synthetic-private-value","properties":{"category":"RemoteTool"}}]}`,
				}},
			}
			var progress strings.Builder
			report := func(message string) { progress.WriteString(message) }
			var err error
			if operation == "deploy" {
				_, err = provider.Deploy(t.Context(), report)
			} else {
				_, err = provider.Preview(t.Context(), report)
			}
			requireConnectionMigrationError(t, err)
			assert.Nil(t, provider.onDiskSource)
			assert.NotContains(t, progress.String(), "synthetic-private-value")
		})
	}
}

func TestExistingProjectHasMutationsOnlyModelsAndAcr(t *testing.T) {
	for _, tt := range []struct {
		name       string
		parameters map[string]any
		want       bool
	}{
		{"reuse", map[string]any{"deployments": []synthesis.Deployment{}}, false},
		{"models", map[string]any{"deployments": []synthesis.Deployment{{Name: "model"}}}, true},
		{"acr", map[string]any{"includeAcr": true}, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, existingProjectHasMutations(&synthesis.Result{Parameters: tt.parameters}))
		})
	}
}
