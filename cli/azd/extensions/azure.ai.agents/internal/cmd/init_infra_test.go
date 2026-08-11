// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"azureaiagent/internal/exterrors"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/pflag"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.yaml.in/yaml/v3"
)

// validFoundryAzureYAML returns an azure.yaml payload exercising the
// synthesizer's two derived parameters: deployments and includeAcr.
// Container deployment via the `docker:` block forces includeAcr=true.
const validFoundryAzureYAML = `name: my-project
metadata:
  template: azure.ai.agents
infra:
  provider: microsoft.foundry
services:
  my-foundry:
    host: azure.ai.project
    deployments:
      - name: gpt-4-1-mini
        model:
          name: gpt-4.1-mini
          format: OpenAI
          version: "2024-07-18"
        sku:
          name: GlobalStandard
          capacity: 50
    agents:
      - name: my-agent
        docker:
          path: src/my-agent
`

func TestEjectInfra_RefusesWhenAzureYamlMissing(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	err := ejectInfra(dir, "bicep")
	require.Error(t, err)

	localErr, ok := errors.AsType[*azdext.LocalError](err)
	require.True(t, ok, "expected structured azdext.LocalError, got %T: %v", err, err)
	assert.Equal(t, exterrors.CodeInfraEjectAzureYamlMissing, localErr.Code)
	assert.Contains(t, localErr.Message, "azure.yaml not found")
	assert.NotEmpty(t, localErr.Suggestion)

	// Refusal must not produce ./infra/.
	_, statErr := os.Stat(filepath.Join(dir, "infra"))
	assert.True(t, os.IsNotExist(statErr), "infra/ must not be created on refusal")
}

func TestEjectInfra_MigratesExistingInfraToLayers(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	mustWriteFile(t, filepath.Join(dir, "azure.yaml"),
		strings.Replace(validFoundryAzureYAML, "provider: microsoft.foundry", "provider: bicep", 1))
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "infra"), 0o750))
	mustWriteFile(t, filepath.Join(dir, "infra", "main.bicep"), "// existing infrastructure\n")

	err := ejectInfra(dir, "bicep")
	require.NoError(t, err)

	existing, err := os.ReadFile(filepath.Join(dir, "infra", "main.bicep")) //nolint:gosec // temp test path
	require.NoError(t, err)
	assert.Equal(t, "// existing infrastructure\n", string(existing))
	assert.FileExists(t, filepath.Join(dir, "infra", "foundry", "main.bicep"))

	raw, err := os.ReadFile(filepath.Join(dir, "azure.yaml")) //nolint:gosec // temp test path
	require.NoError(t, err)
	var doc struct {
		Infra struct {
			Provider string `yaml:"provider"`
			Layers   []struct {
				Name      string   `yaml:"name"`
				Path      string   `yaml:"path"`
				Provider  string   `yaml:"provider"`
				DependsOn []string `yaml:"dependsOn"`
			} `yaml:"layers"`
		} `yaml:"infra"`
	}
	require.NoError(t, yaml.Unmarshal(raw, &doc))
	assert.Empty(t, doc.Infra.Provider)
	require.Len(t, doc.Infra.Layers, 2)
	assert.Equal(t, "infra", doc.Infra.Layers[0].Name)
	assert.Equal(t, "infra", doc.Infra.Layers[0].Path)
	assert.Equal(t, "bicep", doc.Infra.Layers[0].Provider)
	assert.Equal(t, "foundry", doc.Infra.Layers[1].Name)
	assert.Equal(t, "infra/foundry", doc.Infra.Layers[1].Path)
	assert.Equal(t, "microsoft.foundry", doc.Infra.Layers[1].Provider)
	assert.Empty(t, doc.Infra.Layers[0].DependsOn)
	assert.Empty(t, doc.Infra.Layers[1].DependsOn)

	params, err := os.ReadFile(filepath.Join(dir, "infra", "foundry", "main.parameters.json")) //nolint:gosec
	require.NoError(t, err)
	var paramsDoc struct {
		Parameters map[string]struct {
			Value any `json:"value"`
		} `json:"parameters"`
	}
	require.NoError(t, json.Unmarshal(params, &paramsDoc))
	assert.Equal(t, "${AZURE_LOCATION}", paramsDoc.Parameters["location"].Value)
	assert.Equal(t, "${AZURE_AI_PROJECT_NAME=${AZURE_ENV_NAME}}", paramsDoc.Parameters["foundryProjectName"].Value)
	assert.Equal(t, "${AZURE_FOUNDRY_RESOURCE_GROUP=rg-${AZURE_ENV_NAME}-foundry}",
		paramsDoc.Parameters["resourceGroupName"].Value)
	assert.Equal(t, "${AZD_RESOURCE_TOKEN_SALT}", paramsDoc.Parameters["resourceTokenSalt"].Value)
}

func TestEjectInfra_PreservesExistingInfraNameWhenMigratingToLayers(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	mustWriteFile(t, filepath.Join(dir, "azure.yaml"), `name: my-project
infra:
  name: existing
  provider: bicep
  path: infra/existing
services:
  my-foundry:
    host: azure.ai.project
`)
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "infra", "existing"), 0o750))
	mustWriteFile(t, filepath.Join(dir, "infra", "existing", "main.bicep"), "// existing\n")

	require.NoError(t, ejectInfra(dir, "bicep"))
	raw, err := os.ReadFile(filepath.Join(dir, "azure.yaml")) //nolint:gosec
	require.NoError(t, err)
	var doc struct {
		Infra struct {
			Layers []struct {
				Name      string   `yaml:"name"`
				Provider  string   `yaml:"provider"`
				DependsOn []string `yaml:"dependsOn"`
			} `yaml:"layers"`
		} `yaml:"infra"`
	}
	require.NoError(t, yaml.Unmarshal(raw, &doc))
	require.Len(t, doc.Infra.Layers, 2)
	assert.Equal(t, "existing", doc.Infra.Layers[0].Name)
	assert.Equal(t, "foundry", doc.Infra.Layers[1].Name)
	assert.Equal(t, "microsoft.foundry", doc.Infra.Layers[1].Provider)
	assert.Empty(t, doc.Infra.Layers[1].DependsOn)
}

func TestEjectInfra_PreservesAllRootInfraProperties(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	mustWriteFile(t, filepath.Join(dir, "azure.yaml"), `name: my-project
infra:
  name: existing
  provider: custom.platform
  path: infra/existing
  module: platform
  deploymentStacks:
    actionOnUnmanage:
      resources: delete
  config:
    nested:
      enabled: true
  futureProperty:
    value: preserved
services:
  my-foundry:
    host: azure.ai.project
`)

	require.NoError(t, ejectInfra(dir, "bicep"))
	raw, err := os.ReadFile(filepath.Join(dir, "azure.yaml")) //nolint:gosec
	require.NoError(t, err)
	var doc map[string]any
	require.NoError(t, yaml.Unmarshal(raw, &doc))
	layers := doc["infra"].(map[string]any)["layers"].([]any)
	existing := layers[0].(map[string]any)
	assert.Equal(t, "existing", existing["name"])
	assert.Equal(t, "infra/existing", existing["path"])
	assert.Equal(t, "custom.platform", existing["provider"])
	assert.Equal(t, "platform", existing["module"])
	assert.Equal(t, "delete", existing["deploymentStacks"].(map[string]any)["actionOnUnmanage"].(map[string]any)["resources"])
	assert.Equal(t, true, existing["config"].(map[string]any)["nested"].(map[string]any)["enabled"])
	assert.Equal(t, "preserved", existing["futureProperty"].(map[string]any)["value"])
}

func TestEjectInfra_ExistingFoundryLayerReportsAlreadyExists(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	yamlBody := `name: my-project
infra:
  provider: bicep
  layers:
    - name: app
      path: infra/app
    - name: foundry
      path: infra/foundry
      provider: microsoft.foundry
services:
  my-foundry:
    host: azure.ai.project
`
	mustWriteFile(t, filepath.Join(dir, "azure.yaml"), yamlBody)
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "infra", "foundry"), 0o750))
	mustWriteFile(t, filepath.Join(dir, "infra", "foundry", "main.bicep"), "// existing Foundry\n")

	err := ejectInfra(dir, "bicep")
	require.Error(t, err)
	localErr, ok := errors.AsType[*azdext.LocalError](err)
	require.True(t, ok)
	assert.Equal(t, exterrors.CodeInfraEjectExists, localErr.Code)
	assert.Contains(t, localErr.Message, "Foundry infrastructure layer \"foundry\" already exists")
	assert.Contains(t, localErr.Message, "./infra/foundry")
}

func TestEjectInfra_RefusesFoundryProviderUnderDifferentLayerName(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	yamlBody := `name: my-project
infra:
  provider: bicep
  layers:
    - name: app
      path: infra/app
    - name: ai
      path: infra/ai
      provider: microsoft.foundry
services:
  my-foundry:
    host: azure.ai.project
`
	mustWriteFile(t, filepath.Join(dir, "azure.yaml"), yamlBody)

	err := ejectInfra(dir, "bicep")
	require.Error(t, err)
	localErr, ok := errors.AsType[*azdext.LocalError](err)
	require.True(t, ok)
	assert.Contains(t, localErr.Message, "already exists as layer \"ai\"")
	assert.NoDirExists(t, filepath.Join(dir, "infra", "foundry"))
}

func TestEjectInfra_RefusesEditedGeneratedTerraformRoot(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	yamlBody := `name: my-project
infra:
  provider: terraform
services:
  my-foundry:
    host: azure.ai.project
`
	mustWriteFile(t, filepath.Join(dir, "azure.yaml"), yamlBody)
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "infra"), 0o750))
	mustWriteFile(t, filepath.Join(dir, "infra", "main.tf"), "# edited generated terraform\n")
	mustWriteFile(t, filepath.Join(dir, "infra", "main.tfvars.json"), "{}\n")
	mustWriteFile(t, filepath.Join(dir, "infra", foundryTerraformMarker), foundryTerraformV1)

	err := ejectInfra(dir, "terraform")
	require.Error(t, err)
	localErr, ok := errors.AsType[*azdext.LocalError](err)
	require.True(t, ok)
	assert.Equal(t, exterrors.CodeInfraEjectExists, localErr.Code)
	assert.Contains(t, localErr.Message, "Foundry infrastructure already exists")
	assert.NoDirExists(t, filepath.Join(dir, "infra", "foundry"))
}

func TestEjectInfra_MigratesOrdinaryTerraformProjectWithTfvars(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	mustWriteFile(t, filepath.Join(dir, "azure.yaml"), `name: my-project
infra:
  provider: terraform
services:
  my-foundry:
    host: azure.ai.project
`)
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "infra"), 0o750))
	mustWriteFile(t, filepath.Join(dir, "infra", "main.tf"), "resource \"azurerm_resource_group\" \"app\" {}\n")
	mustWriteFile(t, filepath.Join(dir, "infra", "main.tfvars.json"), "{}\n")

	require.NoError(t, ejectInfra(dir, "terraform"))
	assert.FileExists(t, filepath.Join(dir, "infra", "main.tf"))
	assert.FileExists(t, filepath.Join(dir, "infra", "foundry", "main.tf"))
	assert.FileExists(t, filepath.Join(dir, "infra", "foundry", foundryTerraformMarker))
	raw, err := os.ReadFile(filepath.Join(dir, "azure.yaml")) //nolint:gosec
	require.NoError(t, err)
	assert.Contains(t, string(raw), "provider: terraform")
	assert.Contains(t, string(raw), "path: infra/foundry")
}

func TestEjectInfra_RefusesUnknownTerraformMarker(t *testing.T) {
	for _, marker := range []string{"terraform-v2\n", "edited\n", ""} {
		t.Run(strings.TrimSpace(marker), func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			yamlBody := `name: my-project
infra:
  provider: terraform
services:
  my-foundry:
    host: azure.ai.project
`
			mustWriteFile(t, filepath.Join(dir, "azure.yaml"), yamlBody)
			require.NoError(t, os.MkdirAll(filepath.Join(dir, "infra"), 0o750))
			mustWriteFile(t, filepath.Join(dir, "infra", "main.tf"), "# user edited\n")
			mustWriteFile(t, filepath.Join(dir, "infra", foundryTerraformMarker), marker)

			err := ejectInfra(dir, "terraform")
			require.Error(t, err)
			localErr, ok := errors.AsType[*azdext.LocalError](err)
			require.True(t, ok)
			assert.Equal(t, exterrors.CodeInfraEjectMarkerInvalid, localErr.Code)
			assert.Contains(t, localErr.Message, "unsupported or edited")
			assert.NoDirExists(t, filepath.Join(dir, "infra", "foundry"))
			raw, readErr := os.ReadFile(filepath.Join(dir, "azure.yaml")) //nolint:gosec
			require.NoError(t, readErr)
			assert.Equal(t, yamlBody, string(raw))
		})
	}
}

func TestEjectInfra_RefusesNonRegularTerraformMarker(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	mustWriteFile(t, filepath.Join(dir, "azure.yaml"), `name: my-project
infra:
  provider: terraform
services:
  my-foundry:
    host: azure.ai.project
`)
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "infra", foundryTerraformMarker), 0o750))
	mustWriteFile(t, filepath.Join(dir, "infra", "main.tf"), "# user edited\n")

	err := ejectInfra(dir, "terraform")
	require.Error(t, err)
	localErr, ok := errors.AsType[*azdext.LocalError](err)
	require.True(t, ok)
	assert.Equal(t, exterrors.CodeInfraEjectMarkerInvalid, localErr.Code)
	assert.Contains(t, localErr.Message, "not a regular file")
}

func TestEjectInfra_RefusesExplicitFilelessBuiltInProvider(t *testing.T) {
	for _, provider := range []string{"bicep", "terraform"} {
		t.Run(provider, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			yamlBody := `name: my-project
infra:
  provider: ` + provider + `
services:
  my-foundry:
    host: azure.ai.project
`
			mustWriteFile(t, filepath.Join(dir, "azure.yaml"), yamlBody)

			err := ejectInfra(dir, "bicep")
			require.Error(t, err)
			localErr, ok := errors.AsType[*azdext.LocalError](err)
			require.True(t, ok)
			assert.Equal(t, exterrors.CodeInvalidAzureYaml, localErr.Code)
			assert.Contains(t, localErr.Message, "contains no matching entry point")
			assert.NoDirExists(t, filepath.Join(dir, "infra", "foundry"))
			raw, readErr := os.ReadFile(filepath.Join(dir, "azure.yaml")) //nolint:gosec
			require.NoError(t, readErr)
			assert.Equal(t, yamlBody, string(raw))
		})
	}
}

func TestEjectInfra_RefusesExistingFoundryEject(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	mustWriteFile(t, filepath.Join(dir, "azure.yaml"), validFoundryAzureYAML)
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "infra"), 0o750))
	mustWriteFile(t, filepath.Join(dir, "infra", "main.bicep"), "// existing project bicep\n")

	err := ejectInfra(dir, "bicep")
	require.Error(t, err)
	localErr, ok := errors.AsType[*azdext.LocalError](err)
	require.True(t, ok)
	assert.Equal(t, exterrors.CodeInfraEjectExists, localErr.Code)
	assert.Contains(t, localErr.Message, "Foundry infrastructure already exists")
	assert.Contains(t, localErr.Message, "./infra")
	existing, err := os.ReadFile(filepath.Join(dir, "infra", "main.bicep")) //nolint:gosec // temp test path
	require.NoError(t, err)
	assert.Equal(t, "// existing project bicep\n", string(existing))
	assert.NoDirExists(t, filepath.Join(dir, "infra", "foundry"))
}

func TestEjectInfra_FoundryProjectWithEmptyInfraDirectoryUsesLegacyLayout(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	mustWriteFile(t, filepath.Join(dir, "azure.yaml"), validFoundryAzureYAML)
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "infra"), 0o750))

	require.NoError(t, ejectInfra(dir, "bicep"))
	assert.FileExists(t, filepath.Join(dir, "infra", "main.bicep"))
	assert.NoDirExists(t, filepath.Join(dir, "infra", "foundry"))

	raw, err := os.ReadFile(filepath.Join(dir, "azure.yaml")) //nolint:gosec // temp test path
	require.NoError(t, err)
	assert.Equal(t, validFoundryAzureYAML, string(raw))
}

func TestEjectInfra_RefusesNonEmptyInfraWithoutEntrypoint(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	mustWriteFile(t, filepath.Join(dir, "azure.yaml"), validFoundryAzureYAML)
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "infra"), 0o750))
	mustWriteFile(t, filepath.Join(dir, "infra", "README.md"), "user-owned infrastructure notes\n")

	err := ejectInfra(dir, "bicep")
	require.Error(t, err)
	localErr, ok := errors.AsType[*azdext.LocalError](err)
	require.True(t, ok)
	assert.Equal(t, exterrors.CodeInvalidAzureYaml, localErr.Code)
	assert.Contains(t, localErr.Message, "no detectable entry point")
	assert.NoFileExists(t, filepath.Join(dir, "infra", "main.bicep"))
}

func TestEjectInfra_AppendsFoundryLayer(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	mustWriteFile(t, filepath.Join(dir, "azure.yaml"), `name: my-project
infra:
  provider: bicep
  layers:
    - name: platform
      path: infra/platform
      module: platform
services:
  my-foundry:
    host: azure.ai.project
`)
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "infra", "platform"), 0o750))
	mustWriteFile(t, filepath.Join(dir, "infra", "platform", "platform.bicep"), "// platform\n")

	require.NoError(t, ejectInfra(dir, "bicep"))
	assert.FileExists(t, filepath.Join(dir, "infra", "foundry", "main.bicep"))

	raw, err := os.ReadFile(filepath.Join(dir, "azure.yaml")) //nolint:gosec // temp test path
	require.NoError(t, err)
	var doc struct {
		Infra struct {
			Provider string `yaml:"provider"`
			Layers   []struct {
				Name      string   `yaml:"name"`
				Path      string   `yaml:"path"`
				Module    string   `yaml:"module"`
				Provider  string   `yaml:"provider"`
				DependsOn []string `yaml:"dependsOn"`
			} `yaml:"layers"`
		} `yaml:"infra"`
	}
	require.NoError(t, yaml.Unmarshal(raw, &doc))
	assert.Equal(t, "bicep", doc.Infra.Provider)
	require.Len(t, doc.Infra.Layers, 2)
	assert.Equal(t, "platform", doc.Infra.Layers[0].Name)
	assert.Equal(t, "platform", doc.Infra.Layers[0].Module)
	assert.Equal(t, "foundry", doc.Infra.Layers[1].Name)
	assert.Equal(t, "infra/foundry", doc.Infra.Layers[1].Path)
	assert.Equal(t, "microsoft.foundry", doc.Infra.Layers[1].Provider)
	assert.Empty(t, doc.Infra.Layers[0].DependsOn)
	assert.Empty(t, doc.Infra.Layers[1].DependsOn)
}

func TestEjectInfra_RefusesExistingLayerWithoutPath(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	yamlBody := `name: my-project
infra:
  provider: bicep
  layers:
    - name: app
services:
  my-foundry:
    host: azure.ai.project
`
	mustWriteFile(t, filepath.Join(dir, "azure.yaml"), yamlBody)

	err := ejectInfra(dir, "bicep")
	require.Error(t, err)
	localErr, ok := errors.AsType[*azdext.LocalError](err)
	require.True(t, ok)
	assert.Equal(t, exterrors.CodeInvalidAzureYaml, localErr.Code)
	assert.Contains(t, localErr.Message, "must declare path")
	assert.NoDirExists(t, filepath.Join(dir, "infra", "foundry"))
	raw, readErr := os.ReadFile(filepath.Join(dir, "azure.yaml")) //nolint:gosec
	require.NoError(t, readErr)
	assert.Equal(t, yamlBody, string(raw))
}

func TestEjectInfra_MigratesFilelessCustomProviderToLayer(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	mustWriteFile(t, filepath.Join(dir, "azure.yaml"), `name: my-project
infra:
  provider: custom.platform
services:
  my-foundry:
    host: azure.ai.project
`)

	require.NoError(t, ejectInfra(dir, "bicep"))
	raw, err := os.ReadFile(filepath.Join(dir, "azure.yaml")) //nolint:gosec
	require.NoError(t, err)
	assert.Contains(t, string(raw), "provider: custom.platform")
	assert.Contains(t, string(raw), "provider: microsoft.foundry")
	assert.FileExists(t, filepath.Join(dir, "infra", "foundry", "main.bicep"))
}

func TestEjectInfra_HonorsExistingBicepLayerPathAndModule(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	mustWriteFile(t, filepath.Join(dir, "azure.yaml"), `name: my-project
infra:
  provider: bicep
  layers:
    - name: app
      path: infra/app
    - name: foundry
      path: custom/foundry
      module: project
      provider: microsoft.foundry
      dependsOn: [app]
services:
  my-foundry:
    host: azure.ai.project
`)

	require.NoError(t, ejectInfra(dir, "bicep"))
	assert.FileExists(t, filepath.Join(dir, "custom", "foundry", "project.bicep"))
	assert.FileExists(t, filepath.Join(dir, "custom", "foundry", "project.parameters.json"))
	assert.NoFileExists(t, filepath.Join(dir, "custom", "foundry", "main.bicep"))
}

func TestEjectInfra_RefusesNonEmptyDeclaredFoundryTarget(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	yamlBody := `name: my-project
infra:
  provider: bicep
  layers:
    - name: app
      path: infra/app
    - name: foundry
      path: custom/foundry
      provider: microsoft.foundry
services:
  my-foundry:
    host: azure.ai.project
`
	mustWriteFile(t, filepath.Join(dir, "azure.yaml"), yamlBody)
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "custom", "foundry"), 0o750))
	mustWriteFile(t, filepath.Join(dir, "custom", "foundry", "README.md"), "user content\n")

	err := ejectInfra(dir, "bicep")
	require.Error(t, err)
	localErr, ok := errors.AsType[*azdext.LocalError](err)
	require.True(t, ok)
	assert.Equal(t, exterrors.CodeInfraEjectExists, localErr.Code)
	assert.Contains(t, localErr.Message, "Foundry infrastructure layer \"foundry\" already exists")
	assert.NoFileExists(t, filepath.Join(dir, "custom", "foundry", "main.bicep"))
}

func TestAzureYAMLUnchanged(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "azure.yaml")
	mustWriteFile(t, path, "name: original\n")

	unchanged, err := azureYAMLUnchanged(path, []byte("name: original\n"))
	require.NoError(t, err)
	assert.True(t, unchanged)

	mustWriteFile(t, path, "name: changed\n")
	unchanged, err = azureYAMLUnchanged(path, []byte("name: original\n"))
	require.NoError(t, err)
	assert.False(t, unchanged)
}

func TestEjectInfra_RefusesInheritedBicepFoundryLayerProvider(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	mustWriteFile(t, filepath.Join(dir, "azure.yaml"), `name: my-project
infra:
  provider: bicep
  layers:
    - name: app
      path: infra/app
    - name: foundry
      path: infra/foundry
services:
  my-foundry:
    host: azure.ai.project
`)

	err := ejectInfra(dir, "bicep")
	require.Error(t, err)
	localErr, ok := errors.AsType[*azdext.LocalError](err)
	require.True(t, ok)
	assert.Contains(t, localErr.Message, "already uses provider \"bicep\"")
	assert.NoDirExists(t, filepath.Join(dir, "infra", "foundry"))
	raw, err := os.ReadFile(filepath.Join(dir, "azure.yaml")) //nolint:gosec
	require.NoError(t, err)
	assert.NotContains(t, string(raw), "microsoft.foundry")
}

func TestEjectInfra_AppendsIndependentFoundryLayer(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	mustWriteFile(t, filepath.Join(dir, "azure.yaml"), `name: my-project
infra:
  provider: bicep
  layers:
    - name: network
      path: infra/network
    - name: app
      path: infra/app
      dependsOn: [network]
services:
  my-foundry:
    host: azure.ai.project
`)

	require.NoError(t, ejectInfra(dir, "bicep"))
	raw, err := os.ReadFile(filepath.Join(dir, "azure.yaml")) //nolint:gosec // temp test path
	require.NoError(t, err)
	var doc struct {
		Infra struct {
			Layers []struct {
				Name      string   `yaml:"name"`
				DependsOn []string `yaml:"dependsOn"`
			} `yaml:"layers"`
		} `yaml:"infra"`
	}
	require.NoError(t, yaml.Unmarshal(raw, &doc))
	require.Len(t, doc.Infra.Layers, 3)
	assert.Empty(t, doc.Infra.Layers[0].DependsOn)
	assert.Equal(t, []string{"network"}, doc.Infra.Layers[1].DependsOn)
	assert.Empty(t, doc.Infra.Layers[2].DependsOn)
}

func TestEjectInfra_PreservesExistingLayerHooksAndDependencies(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	mustWriteFile(t, filepath.Join(dir, "azure.yaml"), `name: my-project
infra:
  provider: bicep
  layers:
    - name: network
      path: infra/network
      hooks:
        postprovision:
          - run: azd env set VNET_ID value
    - name: app
      path: infra/app
      dependsOn: [network]
services:
  my-foundry:
    host: azure.ai.project
`)

	require.NoError(t, ejectInfra(dir, "bicep"))
	raw, err := os.ReadFile(filepath.Join(dir, "azure.yaml")) //nolint:gosec // temp test path
	require.NoError(t, err)
	var doc struct {
		Infra struct {
			Layers []struct {
				Name      string   `yaml:"name"`
				DependsOn []string `yaml:"dependsOn"`
				Hooks     map[string][]struct {
					Run string `yaml:"run"`
				} `yaml:"hooks"`
			} `yaml:"layers"`
		} `yaml:"infra"`
	}
	require.NoError(t, yaml.Unmarshal(raw, &doc))
	require.Len(t, doc.Infra.Layers, 3)
	assert.Equal(t, "azd env set VNET_ID value", doc.Infra.Layers[0].Hooks["postprovision"][0].Run)
	assert.Empty(t, doc.Infra.Layers[0].DependsOn)
	assert.Equal(t, []string{"network"}, doc.Infra.Layers[1].DependsOn)
	assert.Empty(t, doc.Infra.Layers[2].DependsOn)
}

func TestEjectInfra_RefusesChangingExistingBicepLayerProvider(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	yamlBody := `name: my-project
infra:
  provider: bicep
  layers:
    - name: app
      path: infra/app
    - name: foundry
      path: infra/foundry
      provider: bicep
services:
  my-foundry:
    host: azure.ai.project
`
	mustWriteFile(t, filepath.Join(dir, "azure.yaml"), yamlBody)

	err := ejectInfra(dir, "terraform")
	require.Error(t, err)
	localErr, ok := errors.AsType[*azdext.LocalError](err)
	require.True(t, ok)
	assert.Equal(t, exterrors.CodeInvalidAzureYaml, localErr.Code)
	assert.Contains(t, localErr.Message, "already uses provider")
	assert.NoDirExists(t, filepath.Join(dir, "infra", "foundry"))
}

func TestEjectInfra_RefusesExistingBicepFoundryLayer(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	mustWriteFile(t, filepath.Join(dir, "azure.yaml"), `name: my-project
infra:
  provider: bicep
  layers:
    - name: app
      path: infra/app
    - name: foundry
      path: infra/foundry
      provider: bicep
services:
  my-foundry:
    host: azure.ai.project
`)

	err := ejectInfra(dir, "bicep")
	require.Error(t, err)
	localErr, ok := errors.AsType[*azdext.LocalError](err)
	require.True(t, ok)
	assert.Contains(t, localErr.Message, "already uses provider \"bicep\"")
	assert.NoDirExists(t, filepath.Join(dir, "infra", "foundry"))
}

func TestEjectInfra_AllowsNestedFoundryLayerPath(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	mustWriteFile(t, filepath.Join(dir, "azure.yaml"), `name: my-project
infra:
  provider: bicep
  layers:
    - name: app
      path: infra
services:
  my-foundry:
    host: azure.ai.project
`)

	require.NoError(t, ejectInfra(dir, "bicep"))
	assert.FileExists(t, filepath.Join(dir, "infra", "foundry", "main.bicep"))
}

func TestEjectInfra_MergesNonConflictingUndeclaredFoundryDirectory(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	yamlBody := `name: my-project
infra:
  provider: bicep
  layers:
    - name: app
      path: infra/app
services:
  my-foundry:
    host: azure.ai.project
`
	mustWriteFile(t, filepath.Join(dir, "azure.yaml"), yamlBody)
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "infra", "foundry"), 0o750))
	mustWriteFile(t, filepath.Join(dir, "infra", "foundry", "README.md"), "keep me\n")

	require.NoError(t, ejectInfra(dir, "bicep"))
	assert.FileExists(t, filepath.Join(dir, "infra", "foundry", "main.bicep"))
	readme, err := os.ReadFile(filepath.Join(dir, "infra", "foundry", "README.md")) //nolint:gosec
	require.NoError(t, err)
	assert.Equal(t, "keep me\n", string(readme))
}

func TestEjectInfra_RefusesGeneratedFileConflictWithoutUpdatingAzureYaml(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	yamlBody := `name: my-project
infra:
  provider: bicep
  layers:
    - name: app
      path: infra/app
services:
  my-foundry:
    host: azure.ai.project
`
	mustWriteFile(t, filepath.Join(dir, "azure.yaml"), yamlBody)
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "infra", "foundry"), 0o750))
	mustWriteFile(t, filepath.Join(dir, "infra", "foundry", "main.bicep"), "// conflict\n")

	err := ejectInfra(dir, "bicep")
	require.Error(t, err)
	localErr, ok := errors.AsType[*azdext.LocalError](err)
	require.True(t, ok)
	assert.Equal(t, exterrors.CodeInfraEjectExists, localErr.Code)
	assert.Contains(t, localErr.Message, "./infra/foundry/main.bicep")

	raw, readErr := os.ReadFile(filepath.Join(dir, "azure.yaml")) //nolint:gosec // temp test path
	require.NoError(t, readErr)
	assert.Equal(t, yamlBody, string(raw))
	conflict, readErr := os.ReadFile(filepath.Join(dir, "infra", "foundry", "main.bicep")) //nolint:gosec
	require.NoError(t, readErr)
	assert.Equal(t, "// conflict\n", string(conflict))
}

func TestEjectInfra_RefusesFoundryLayerOutsideProject(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	mustWriteFile(t, filepath.Join(dir, "azure.yaml"), `name: my-project
infra:
  layers:
    - name: app
      path: infra/app
      provider: bicep
    - name: foundry
      path: ../foundry
      provider: microsoft.foundry
      dependsOn: [app]
services:
  my-foundry:
    host: azure.ai.project
`)

	err := ejectInfra(dir, "bicep")
	require.Error(t, err)
	localErr, ok := errors.AsType[*azdext.LocalError](err)
	require.True(t, ok)
	assert.Equal(t, exterrors.CodeInvalidAzureYaml, localErr.Code)
	assert.Contains(t, localErr.Message, "must not contain '..'")
	assert.NoDirExists(t, filepath.Join(filepath.Dir(dir), "foundry"))
}

func TestEjectInfra_RefusesWhenNoFoundryService(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		yaml string
	}{
		{
			name: "non-foundry services only",
			yaml: `name: my-project
services:
  webapp:
    host: containerapp
    project: src/web
`,
		},
		{
			name: "no services block at all",
			yaml: `name: my-project
infra:
  provider: microsoft.foundry
`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			mustWriteFile(t, filepath.Join(dir, "azure.yaml"), tt.yaml)

			err := ejectInfra(dir, "bicep")
			require.Error(t, err)

			localErr, ok := errors.AsType[*azdext.LocalError](err)
			require.True(t, ok, "expected structured azdext.LocalError, got %T", err)
			assert.Equal(t, exterrors.CodeInfraEjectNoFoundryService, localErr.Code)
			assert.Contains(t, localErr.Message, "nothing to eject")

			_, statErr := os.Stat(filepath.Join(dir, "infra"))
			assert.True(t, os.IsNotExist(statErr), "infra/ must not be created on refusal")
		})
	}
}

func TestEjectInfra_RefusesWhenMultipleFoundryServices(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	mustWriteFile(t, filepath.Join(dir, "azure.yaml"), `name: my-project
services:
  agent-a:
    host: azure.ai.project
  agent-b:
    host: azure.ai.project
`)

	err := ejectInfra(dir, "bicep")
	require.Error(t, err)

	localErr, ok := errors.AsType[*azdext.LocalError](err)
	require.True(t, ok)
	assert.Equal(t, exterrors.CodeInfraEjectMultipleFoundryServices, localErr.Code)
	assert.Contains(t, localErr.Message, "multiple services")
	// Deterministic ordering check: matches are sorted before formatting.
	assert.Contains(t, localErr.Message, "[agent-a agent-b]")
}

func TestEjectInfra_RefusesWhenBrownfieldEndpoint(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	mustWriteFile(t, filepath.Join(dir, "azure.yaml"), `name: my-project
services:
  ai-project:
    host: azure.ai.project
    endpoint: https://acct.services.ai.azure.com/api/projects/p1
`)

	err := ejectInfra(dir, "bicep")
	require.Error(t, err)

	localErr, ok := errors.AsType[*azdext.LocalError](err)
	require.True(t, ok)
	assert.Equal(t, exterrors.CodeInfraEjectBrownfieldUnsupported, localErr.Code)
	assert.Contains(t, localErr.Message, "endpoint:")
}

func TestEjectInfra_HappyPath_WritesExpectedFiles(t *testing.T) {
	// Intentionally NOT parallel: this test captures os.Stdout, and running
	// it concurrently with other stdout-capturing tests in the same package
	// would race over the global file descriptor.
	dir := t.TempDir()
	mustWriteFile(t, filepath.Join(dir, "azure.yaml"), validFoundryAzureYAML)

	stdout := withCapturedStdout(t, func() {
		err := ejectInfra(dir, "bicep")
		require.NoError(t, err)
	})

	// Every embedded template under templates/ (except main.arm.json and the
	// dead-in-a-greenfield-eject brownfield.bicep/brownfield.arm.json) should
	// be on disk under ./infra/, plus the synthesized main.parameters.json.
	expected := []string{
		filepath.Join("infra", "main.bicep"),
		filepath.Join("infra", "abbreviations.json"),
		filepath.Join("infra", "modules", "acr.bicep"),
		filepath.Join("infra", "modules", "connections.bicep"),
		filepath.Join("infra", "modules", "network.bicep"),
		filepath.Join("infra", "modules", "subnet.bicep"),
		filepath.Join("infra", "modules", "private-endpoint-dns.bicep"),
		filepath.Join("infra", "main.parameters.json"),
	}
	for _, rel := range expected {
		path := filepath.Join(dir, rel)
		info, err := os.Stat(path)
		require.NoError(t, err, "expected file %s", rel)
		assert.Greater(t, info.Size(), int64(0), "file %s should not be empty", rel)
	}

	// main.arm.json is deliberately excluded.
	_, err := os.Stat(filepath.Join(dir, "infra", "main.arm.json"))
	assert.True(t, os.IsNotExist(err),
		"main.arm.json should be excluded from the ejected tree (it would be stale "+
			"the moment the user edits main.bicep)")

	// brownfield.bicep/brownfield.arm.json are excluded too: unreachable in a
	// greenfield eject (see TestEjectInfra_RefusesWhenBrownfieldEndpoint).
	for _, rel := range []string{
		filepath.Join("infra", "brownfield.bicep"),
		filepath.Join("infra", "brownfield.arm.json"),
	} {
		_, err := os.Stat(filepath.Join(dir, rel))
		assert.True(t, os.IsNotExist(err),
			"%s should be excluded from the ejected tree (unused in a greenfield eject)", rel)
	}

	// Spec's success block elements.
	assert.Contains(t, stdout, "Generating infrastructure files from azure.yaml")
	assert.Contains(t, stdout, "infra/main.bicep")
	assert.Contains(t, stdout, "infra/modules/acr.bicep")
	assert.Contains(t, stdout, "infra/main.parameters.json")
	assert.Contains(t, stdout, "Future provisions will read the Foundry layer from ./infra/")
	assert.Contains(t, stdout, "Next steps:")
	assert.Contains(t, stdout, "azd provision")

	// azure.yaml must not be mutated by eject (spec is explicit on this).
	got, err := os.ReadFile(filepath.Join(dir, "azure.yaml")) //nolint:gosec // G304: test file path from t.TempDir()
	require.NoError(t, err)
	assert.Equal(t, validFoundryAzureYAML, string(got),
		"azure.yaml must not be mutated by eject")
}

func TestEjectInfra_HappyPath_ParametersFileShape(t *testing.T) {
	// See TestEjectInfra_HappyPath_WritesExpectedFiles for why this is not parallel.
	dir := t.TempDir()
	mustWriteFile(t, filepath.Join(dir, "azure.yaml"), validFoundryAzureYAML)

	withCapturedStdout(t, func() {
		require.NoError(t, ejectInfra(dir, "bicep"))
	})

	raw, err := os.ReadFile(filepath.Join(dir, "infra", "main.parameters.json")) //nolint:gosec // G304: test file path from t.TempDir()
	require.NoError(t, err)

	var doc struct {
		Schema         string `json:"$schema"`
		ContentVersion string `json:"contentVersion"`
		Parameters     map[string]struct {
			Value any `json:"value"`
		} `json:"parameters"`
	}
	require.NoError(t, json.Unmarshal(raw, &doc),
		"main.parameters.json must be valid JSON")

	assert.Contains(t, doc.Schema, "deploymentParameters.json",
		"$schema must point at the ARM parameters schema")
	assert.Equal(t, "1.0.0.0", doc.ContentVersion)

	// Synthesizer derives exactly these two from the test YAML: includeAcr
	// because of the docker: block, and a single deployment entry.
	require.Contains(t, doc.Parameters, "includeAcr")
	assert.Equal(t, true, doc.Parameters["includeAcr"].Value)

	require.Contains(t, doc.Parameters, "deployments")
	deps, ok := doc.Parameters["deployments"].Value.([]any)
	require.True(t, ok, "deployments should be an array, got %T",
		doc.Parameters["deployments"].Value)
	require.Len(t, deps, 1)

	// Deploy-time-only params that we intentionally omit so the file isn't
	// stale the moment the user runs `azd env new`.
	for _, k := range []string{
		"location", "foundryProjectName", "resourceTokenSalt",
		"principalId", "tags",
	} {
		assert.NotContains(t, doc.Parameters, k,
			"%s is supplied at provision time and must not be hard-coded in the ejected file", k)
	}
}

func TestEjectInfra_HappyPath_NoDockerOmitsAcrParam(t *testing.T) {
	// See TestEjectInfra_HappyPath_WritesExpectedFiles for why this is not parallel.
	dir := t.TempDir()
	// No docker: block -> includeAcr should be false in the params file
	// but the acr.bicep module is still written (the template files are a
	// static set; whether ACR is provisioned is a parameter decision).
	mustWriteFile(t, filepath.Join(dir, "azure.yaml"), `name: my-project
services:
  my-foundry:
    host: azure.ai.project
    deployments: []
    agents:
      - name: my-agent
        image: registry.io/myorg/myagent:latest
`)

	withCapturedStdout(t, func() {
		require.NoError(t, ejectInfra(dir, "bicep"))
	})

	// acr.bicep is still in the ejected tree -- the template is static.
	_, err := os.Stat(filepath.Join(dir, "infra", "modules", "acr.bicep"))
	assert.NoError(t, err, "acr.bicep module is part of the static template set")

	raw, err := os.ReadFile(filepath.Join(dir, "infra", "main.parameters.json")) //nolint:gosec // G304: test file path from t.TempDir()
	require.NoError(t, err)
	var doc struct {
		Parameters map[string]struct {
			Value any `json:"value"`
		} `json:"parameters"`
	}
	require.NoError(t, json.Unmarshal(raw, &doc))
	assert.Equal(t, false, doc.Parameters["includeAcr"].Value)
}

func TestEjectInfra_EjectsConnectionServices(t *testing.T) {
	// See TestEjectInfra_HappyPath_WritesExpectedFiles for why this is not parallel.
	// Connection metadata and credentials are ejected separately.
	// Bicep keeps credential values in a secure object parameter.
	dir := t.TempDir()
	mustWriteFile(t, filepath.Join(dir, "azure.yaml"), `name: my-project
services:
  my-foundry:
    host: azure.ai.project
    deployments: []
  search-conn:
    host: azure.ai.connection
    uses: [my-foundry]
    category: CognitiveSearch
    target: https://my-search.search.windows.net
    authType: ApiKey
    credentials:
      key: ${SEARCH_API_KEY}
  mcp-conn:
    host: azure.ai.connection
    uses: [my-foundry]
    category: RemoteTool
    target: https://mcp.example.com
    authType: CustomKeys
    credentials:
      keys:
        x-api-key: ${MCP_KEY}
`)

	withCapturedStdout(t, func() {
		require.NoError(t, ejectInfra(dir, "bicep"))
	})

	// The connections module is part of the ejected tree.
	_, err := os.Stat(filepath.Join(dir, "infra", "modules", "connections.bicep"))
	assert.NoError(t, err, "connections.bicep module must be ejected")

	raw, err := os.ReadFile(filepath.Join(dir, "infra", "main.parameters.json")) //nolint:gosec // G304: test file path from t.TempDir()
	require.NoError(t, err)
	var doc struct {
		Parameters map[string]struct {
			Value any `json:"value"`
		} `json:"parameters"`
	}
	require.NoError(t, json.Unmarshal(raw, &doc))

	require.Contains(t, doc.Parameters, "connections")
	conns, ok := doc.Parameters["connections"].Value.([]any)
	require.True(t, ok, "connections should be an array, got %T", doc.Parameters["connections"].Value)
	require.Len(t, conns, 2)

	conn, ok := conns[1].(map[string]any)
	require.True(t, ok, "connection entry should be an object, got %T", conns[0])
	assert.Equal(t, "search-conn", conn["name"])
	assert.Equal(t, "CognitiveSearch", conn["category"])
	assert.Equal(t, "ApiKey", conn["authType"])

	assert.NotContains(t, conn, "credentials")

	// Nested CustomKeys credentials must remain an object so Terraform's
	// optional(any) value can preserve mixed connection credential shapes.
	mcpConn, ok := conns[0].(map[string]any)
	require.True(t, ok, "connection entry should be an object, got %T", conns[0])
	assert.Equal(t, "mcp-conn", mcpConn["name"])
	assert.NotContains(t, mcpConn, "credentials")

	secureCreds, ok := doc.Parameters["connectionCredentials"].Value.(map[string]any)
	require.True(t, ok, "connectionCredentials should be an object")
	searchCreds := secureCreds["search-conn"].(map[string]any)
	assert.Equal(t, "${SEARCH_API_KEY}", searchCreds["key"])
	mcpCreds := secureCreds["mcp-conn"].(map[string]any)
	keys := mcpCreds["keys"].(map[string]any)
	assert.Equal(t, "${MCP_KEY}", keys["x-api-key"])
}

func TestEjectInfra_PreservesNetworkVarRefs(t *testing.T) {
	// See TestEjectInfra_HappyPath_WritesExpectedFiles for why this is not parallel.
	// Eject must keep ${VAR} references verbatim in main.parameters.json so the
	// ejected tree stays environment-portable; the on-disk provision flow
	// resolves them from the azd environment at provision time.
	dir := t.TempDir()
	mustWriteFile(t, filepath.Join(dir, "azure.yaml"), `name: my-project
services:
  my-foundry:
    host: azure.ai.project
    network:
      peSubnet: {vnet: "${AZURE_VNET_ID}", name: pe-subnet}
      dns:
        resourceGroup: rg-dns
        subscription: "${AZURE_DNS_SUBSCRIPTION_ID}"
    deployments: []
    agents:
      - name: my-agent
        image: registry.io/myorg/myagent:latest
`)

	withCapturedStdout(t, func() {
		require.NoError(t, ejectInfra(dir, "bicep"))
	})

	raw, err := os.ReadFile(filepath.Join(dir, "infra", "main.parameters.json")) //nolint:gosec // G304: test file path from t.TempDir()
	require.NoError(t, err)
	var doc struct {
		Parameters map[string]struct {
			Value any `json:"value"`
		} `json:"parameters"`
	}
	require.NoError(t, json.Unmarshal(raw, &doc))

	assert.Equal(t, "${AZURE_VNET_ID}", doc.Parameters["vnetId"].Value,
		"vnet id ${VAR} must be preserved for provision-time resolution")
	assert.Equal(t, "${AZURE_DNS_SUBSCRIPTION_ID}", doc.Parameters["dnsZonesSubscription"].Value,
		"dns subscription ${VAR} must be preserved for provision-time resolution")
	assert.Equal(t, true, doc.Parameters["enableNetworkIsolation"].Value)

	// Managed egress (no agentSubnet): the full param set must thread through.
	assert.Equal(t, true, doc.Parameters["useManagedEgress"].Value,
		"omitting agentSubnet selects managed egress")
	assert.Equal(t, false, doc.Parameters["createAgentSubnet"].Value,
		"managed egress creates no agent subnet")
	assert.Equal(t, "pe-subnet", doc.Parameters["peSubnetName"].Value)
	assert.Equal(t, false, doc.Parameters["createPESubnet"].Value,
		"peSubnet without prefix references an existing subnet")
	assert.Equal(t, "rg-dns", doc.Parameters["dnsZonesResourceGroup"].Value,
		"dns.resourceGroup selects reference mode")
}

// TestEjectInfra_Bicep_NetworkParamsComplete_Byo ejects a BYO-egress service
// (agentSubnet + peSubnet, both with prefixes) and asserts the complete network
// parameter set lands in main.parameters.json. This is the Bicep eject path's
// end-to-end contract: every value the synthesizer derives from network: must
// reach the ejected parameters file so a later `azd provision` reproduces the
// declared topology.
func TestEjectInfra_Bicep_NetworkParamsComplete_Byo(t *testing.T) {
	// Not parallel: shares the stdout-capture rationale of the other eject tests.
	dir := t.TempDir()
	mustWriteFile(t, filepath.Join(dir, "azure.yaml"), `name: my-project
infra:
  provider: microsoft.foundry
services:
  my-foundry:
    host: azure.ai.project
    network:
      agentSubnet:
        vnet: "${AZURE_VNET_ID}"
        name: agent-subnet
        prefix: 192.168.10.0/24
      peSubnet:
        vnet: "${AZURE_VNET_ID}"
        name: pe-subnet
        prefix: 192.168.11.0/24
    deployments: []
    agents:
      - name: my-agent
        image: registry.io/myorg/myagent:latest
`)

	withCapturedStdout(t, func() {
		require.NoError(t, ejectInfra(dir, "bicep"))
	})

	raw, err := os.ReadFile(filepath.Join(dir, "infra", "main.parameters.json")) //nolint:gosec // G304: test file path from t.TempDir()
	require.NoError(t, err)
	var doc struct {
		Parameters map[string]struct {
			Value any `json:"value"`
		} `json:"parameters"`
	}
	require.NoError(t, json.Unmarshal(raw, &doc))

	// Ingress + egress are both private; agentSubnet present => BYO egress.
	assert.Equal(t, true, doc.Parameters["enableNetworkIsolation"].Value)
	assert.Equal(t, false, doc.Parameters["useManagedEgress"].Value,
		"agentSubnet present selects BYO egress")
	assert.Equal(t, "${AZURE_VNET_ID}", doc.Parameters["vnetId"].Value,
		"vnet id ${VAR} must be preserved for provision-time resolution")

	// Agent (egress) subnet: prefix set => create.
	assert.Equal(t, "agent-subnet", doc.Parameters["agentSubnetName"].Value)
	assert.Equal(t, "192.168.10.0/24", doc.Parameters["agentSubnetPrefix"].Value)
	assert.Equal(t, true, doc.Parameters["createAgentSubnet"].Value,
		"agentSubnet with a prefix is created")

	// PE (ingress) subnet: prefix set => create.
	assert.Equal(t, "pe-subnet", doc.Parameters["peSubnetName"].Value)
	assert.Equal(t, "192.168.11.0/24", doc.Parameters["peSubnetPrefix"].Value)
	assert.Equal(t, true, doc.Parameters["createPESubnet"].Value,
		"peSubnet with a prefix is created")

	// BYO egress has no managed-network knobs.
	assert.Equal(t, "", doc.Parameters["managedIsolationMode"].Value,
		"isolationMode is managed-egress only")
}

func TestEjectInfra_RefusesWhenInfraIsAFile(t *testing.T) {
	t.Parallel()
	// Pre-existing `infra` as a regular file cannot be used as the generated
	// directory and must never be overwritten.
	dir := t.TempDir()
	mustWriteFile(t, filepath.Join(dir, "azure.yaml"), validFoundryAzureYAML)
	mustWriteFile(t, filepath.Join(dir, "infra"), "this is a file, not a dir")

	err := ejectInfra(dir, "bicep")
	require.Error(t, err)

	localErr, ok := errors.AsType[*azdext.LocalError](err)
	require.True(t, ok)
	assert.Equal(t, exterrors.CodeInfraEjectExists, localErr.Code,
		"a pre-existing file at ./infra is reported as an exists conflict, "+
			"not silently overwritten")

	// User's file must survive the refusal.
	got, err := os.ReadFile(filepath.Join(dir, "infra")) //nolint:gosec // G304: test file path from t.TempDir()
	require.NoError(t, err)
	assert.Equal(t, "this is a file, not a dir", string(got))
}

func TestEjectInfra_RefusesSymlinkedFoundryPath(t *testing.T) {
	t.Parallel()
	projectRoot := t.TempDir()
	outside := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(projectRoot, "infra"), 0o750))
	if err := os.Symlink(outside, filepath.Join(projectRoot, "infra", "foundry")); err != nil {
		t.Skipf("symlink creation unavailable: %v", err)
	}
	mustWriteFile(t, filepath.Join(projectRoot, "azure.yaml"), `name: my-project
infra:
  layers:
    - name: app
      path: infra/app
      provider: bicep
services:
  my-foundry:
    host: azure.ai.project
`)

	err := ejectInfra(projectRoot, "bicep")
	require.Error(t, err)
	localErr, ok := errors.AsType[*azdext.LocalError](err)
	require.True(t, ok)
	assert.Equal(t, exterrors.CodeInvalidAzureYaml, localErr.Code)
	assert.Contains(t, localErr.Message, "escapes project root")
	assert.NoFileExists(t, filepath.Join(outside, "main.bicep"))
}

func TestValidateStandaloneEjectArgs(t *testing.T) {
	tests := []struct {
		name      string
		args      []string
		changed   map[string]string
		wantError bool
		wantInput string
	}{
		{name: "no extras: ok"},
		{name: "positional arg: refuse", args: []string{"./foo"}, wantError: true, wantInput: "positional path"},
		{
			name:      "manifest flag: refuse",
			changed:   map[string]string{"manifest": "./agent.yaml"},
			wantError: true,
			wantInput: "--manifest",
		},
		{
			name:      "scalar init flag: refuse",
			changed:   map[string]string{"model": "gpt-5.4-mini"},
			wantError: true,
			wantInput: "--model",
		},
		{
			name:      "slice init flag: refuse",
			changed:   map[string]string{"protocol": "responses"},
			wantError: true,
			wantInput: "--protocol",
		},
		{
			name:      "boolean init flag: refuse",
			changed:   map[string]string{"force": "true"},
			wantError: true,
			wantInput: "--force",
		},
		{
			name:      "environment flag: refuse",
			changed:   map[string]string{"environment": "dev"},
			wantError: true,
			wantInput: "--environment",
		},
		{
			name: "global execution controls: ok",
			changed: map[string]string{
				"cwd":       ".",
				"debug":     "true",
				"infra":     "bicep",
				"no-prompt": "true",
				"output":    "json",
			},
		},
		{
			name:      "multiple inputs: refuse",
			args:      []string{"./pos"},
			changed:   map[string]string{"manifest": "./agent.yaml", "src": "./src"},
			wantError: true,
			wantInput: "--manifest",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			cmd := newInitCommand(&azdext.ExtensionContext{})
			// These are inherited global flags on the real extension command.
			// Register them locally here so the standalone helper sees the same
			// changed-flag state without constructing the full command tree.
			if cmd.Flags().Lookup("cwd") == nil {
				cmd.Flags().String("cwd", "", "")
				cmd.Flags().Bool("debug", false, "")
				cmd.Flags().String("environment", "", "")
				cmd.Flags().Bool("no-prompt", false, "")
				cmd.Flags().String("output", "default", "")
			}
			for name, value := range tt.changed {
				require.NoError(t, cmd.Flags().Set(name, value))
			}

			err := validateStandaloneEjectArgs(cmd, tt.args)
			if !tt.wantError {
				assert.NoError(t, err)
				return
			}
			require.Error(t, err)
			localErr, ok := errors.AsType[*azdext.LocalError](err)
			require.True(t, ok, "expected *azdext.LocalError, got %T", err)
			assert.Equal(t, exterrors.CodeInfraEjectConflictingArguments, localErr.Code)
			assert.Equal(t, azdext.LocalErrorCategoryValidation, localErr.Category,
				"the conflict is bad-user-input, classified Validation")
			assert.Contains(t, localErr.Message, tt.wantInput)
			// Suggestion must point at both ways out: drop the arg, or drop --infra.
			assert.Contains(t, localErr.Suggestion, "drop the init inputs")
			assert.Contains(t, localErr.Suggestion, "remove --infra")
		})
	}
}

func TestValidateStandaloneEjectArgs_AllowsSDKTraceFlags(t *testing.T) {
	// Not parallel: NewRootCommand enables Cobra's package-level traverse-run
	// hooks while constructing the real extension command tree.
	root := NewRootCommand()
	initCmd, _, err := root.Find([]string{"init"})
	require.NoError(t, err)
	require.NotNil(t, initCmd)

	require.NotNil(t, initCmd.InheritedFlags().Lookup("trace-log-file"))
	require.NotNil(t, initCmd.InheritedFlags().Lookup("trace-log-url"))
	require.NoError(t, initCmd.InheritedFlags().Set("trace-log-file", "trace.jsonl"))
	require.NoError(t, initCmd.InheritedFlags().Set("trace-log-url", "http://localhost:4318"))
	require.NoError(t, initCmd.Flags().Set("infra", "bicep"))

	var visited []string
	initCmd.InheritedFlags().Visit(func(flag *pflag.Flag) {
		visited = append(visited, flag.Name)
	})
	assert.Contains(t, visited, "trace-log-file", "the test must exercise changed inherited flags")
	assert.Contains(t, visited, "trace-log-url", "the test must exercise changed inherited flags")

	assert.NoError(t, validateStandaloneEjectArgs(initCmd, nil),
		"SDK tracing and --infra are execution controls, not discarded init inputs")
}

func TestParseInfraProvider(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		in      string
		want    string
		wantErr bool
	}{
		{name: "bicep", in: "bicep", want: "bicep"},
		{name: "terraform", in: "terraform", want: "terraform"},
		{name: "uppercase terraform", in: "TERRAFORM", want: "terraform"},
		{name: "mixed case bicep", in: "Bicep", want: "bicep"},
		{name: "whitespace trimmed", in: "  terraform  ", want: "terraform"},
		{name: "unknown value", in: "pulumi", wantErr: true},
		{name: "arm not supported", in: "arm", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := parseInfraProvider(tt.in)
			if tt.wantErr {
				require.Error(t, err)
				localErr, ok := errors.AsType[*azdext.LocalError](err)
				require.True(t, ok, "expected *azdext.LocalError, got %T", err)
				assert.Equal(t, exterrors.CodeInvalidParameter, localErr.Code)
				assert.Contains(t, localErr.Suggestion, "--infra=bicep")
				assert.Contains(t, localErr.Suggestion, "--infra=terraform")
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestEjectInfraAfterInit_ResolvesParentProject(t *testing.T) {
	t.Setenv("AZD_EXEC_PROJECT_DIR", "")
	projectRoot := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(projectRoot, "azure.yaml"), []byte(`name: test
services:
  ai-project:
    host: azure.ai.project
`), 0600))
	nestedDir := filepath.Join(projectRoot, "src", "agent")
	require.NoError(t, os.MkdirAll(nestedDir, 0750))
	t.Chdir(nestedDir)

	withCapturedStdout(t, func() {
		require.NoError(t, ejectInfraAfterInit("bicep"))
	})

	assert.FileExists(t, filepath.Join(projectRoot, "infra", "main.bicep"))
	assert.NoDirExists(t, filepath.Join(nestedDir, "infra"))
}

func TestInitInfra_StandaloneEjectDelegatesToProjects(t *testing.T) {
	t.Setenv("AZD_EXEC_PROJECT_DIR", "")
	projectRoot := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(projectRoot, "azure.yaml"), []byte(`name: test
services:
  ai-project:
    host: azure.ai.project
`), 0600))
	nestedDir := filepath.Join(projectRoot, "src", "agent")
	require.NoError(t, os.MkdirAll(nestedDir, 0750))
	t.Chdir(nestedDir)
	server := &recordingProjectServer{}
	_, serverAddress := newProjectRecorderClientWithAddress(t, server)
	t.Setenv("AZD_SERVER", serverAddress)

	cmd := newInitCommand(&azdext.ExtensionContext{})
	cmd.SetArgs([]string{"--infra"})
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)

	var execErr error
	withCapturedStdout(t, func() {
		execErr = cmd.Execute()
	})

	require.NoError(t, execErr)
	server.mu.Lock()
	defer server.mu.Unlock()
	require.Len(t, server.delegatedRequests, 1)
	infra := server.delegatedRequests[0]["infra"].(map[string]any)
	assert.Equal(t, "bicep", infra["ejectProvider"])
	assert.NoDirExists(t, filepath.Join(projectRoot, "infra"))
	assert.NoDirExists(t, filepath.Join(nestedDir, "infra"))
}

func TestEjectInfraAfterInit_NoProject(t *testing.T) {
	t.Setenv("AZD_EXEC_PROJECT_DIR", "")
	t.Chdir(t.TempDir())

	assert.NoError(t, ejectInfraAfterInit("bicep"))
}

func TestEjectInfraAfterInit_SkipsProjectWithoutFoundryService(t *testing.T) {
	t.Setenv("AZD_EXEC_PROJECT_DIR", "")
	projectRoot := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(projectRoot, "azure.yaml"), []byte(`name: test
services:
  web:
    host: containerapp
`), 0600))
	t.Chdir(projectRoot)

	assert.NoError(t, ejectInfraAfterInit("bicep"))
	assert.NoDirExists(t, filepath.Join(projectRoot, "infra"))
}

func TestEjectInfraAfterInit_PropagatesInvalidFoundryConfiguration(t *testing.T) {
	t.Setenv("AZD_EXEC_PROJECT_DIR", "")
	projectRoot := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(projectRoot, "azure.yaml"), []byte(`name: test
services:
  first-project:
    host: azure.ai.project
  second-project:
    host: azure.ai.project
`), 0600))
	t.Chdir(projectRoot)

	err := ejectInfraAfterInit("bicep")
	require.Error(t, err)
	localErr, ok := errors.AsType[*azdext.LocalError](err)
	require.True(t, ok, "expected *azdext.LocalError, got %T", err)
	assert.Equal(t, exterrors.CodeInfraEjectMultipleFoundryServices, localErr.Code)
	assert.NoDirExists(t, filepath.Join(projectRoot, "infra"))
}

func TestHasFoundryServiceForEject(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		yaml        string
		omitYAML    bool
		want        bool
		wantErrCode string
	}{
		{name: "foundry project service", yaml: validFoundryAzureYAML, want: true},
		{
			name: "legacy foundry host",
			yaml: `name: my-project
services:
  agent:
    host: azure.ai.agent
`,
			want: true,
		},
		{
			name: "non-foundry services only",
			yaml: `name: my-project
services:
  web:
    host: containerapp
    project: src/web
`,
			want: false,
		},
		{name: "no services block", yaml: "name: my-project\n", want: false},
		{
			name: "multiple foundry services",
			yaml: `name: my-project
services:
  first:
    host: azure.ai.project
  second:
    host: azure.ai.project
`,
			wantErrCode: exterrors.CodeInfraEjectMultipleFoundryServices,
		},
		{name: "azure.yaml missing", omitYAML: true, wantErrCode: exterrors.CodeInfraEjectAzureYamlMissing},
		{
			name:        "malformed yaml",
			yaml:        "name: my-project\nservices: [oops\n",
			wantErrCode: exterrors.CodeInvalidAzureYaml,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			if !tt.omitYAML {
				mustWriteFile(t, filepath.Join(dir, "azure.yaml"), tt.yaml)
			}

			got, err := hasFoundryServiceForEject(dir)
			if tt.wantErrCode != "" {
				require.Error(t, err)
				localErr, ok := errors.AsType[*azdext.LocalError](err)
				require.True(t, ok, "expected *azdext.LocalError, got %T", err)
				assert.Equal(t, tt.wantErrCode, localErr.Code)
				assert.False(t, got)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestEnsureInfraDirAbsent(t *testing.T) {
	t.Parallel()

	t.Run("absent", func(t *testing.T) {
		t.Parallel()
		assert.NoError(t, ensureInfraDirAbsent(t.TempDir()))
	})

	t.Run("directory present", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		require.NoError(t, os.MkdirAll(filepath.Join(dir, "infra"), 0o750))

		err := ensureInfraDirAbsent(dir)
		require.Error(t, err)
		localErr, ok := errors.AsType[*azdext.LocalError](err)
		require.True(t, ok, "expected *azdext.LocalError, got %T", err)
		assert.Equal(t, exterrors.CodeInfraEjectExists, localErr.Code)
	})

	t.Run("file present", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		mustWriteFile(t, filepath.Join(dir, "infra"), "not a dir")

		err := ensureInfraDirAbsent(dir)
		require.Error(t, err)
		localErr, ok := errors.AsType[*azdext.LocalError](err)
		require.True(t, ok, "expected *azdext.LocalError, got %T", err)
		assert.Equal(t, exterrors.CodeInfraEjectExists, localErr.Code)
	})

	t.Run("dangling symlink present", func(t *testing.T) {
		t.Parallel()
		if runtime.GOOS == "windows" {
			t.Skip("creating symlinks requires elevated privileges on some Windows hosts")
		}

		dir := t.TempDir()
		require.NoError(t, os.Symlink("missing-target", filepath.Join(dir, "infra")))

		err := ensureInfraDirAbsent(dir)
		require.Error(t, err)
		localErr, ok := errors.AsType[*azdext.LocalError](err)
		require.True(t, ok, "expected *azdext.LocalError, got %T", err)
		assert.Equal(t, exterrors.CodeInfraEjectExists, localErr.Code)
	})
}

// A project can point azd at IaC outside ./infra/ with `infra.path`. Eject
// always writes ./infra/, and the Terraform path drops `infra.path` on its way
// out, so such a project has to be refused before anything is written — an
// absent ./infra/ says nothing about whether the project owns infrastructure.
func TestEnsureDefaultInfraPath(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		yaml     string
		wantCode string
	}{
		{name: "no infra block", yaml: "name: my-project\n"},
		{name: "infra block without path", yaml: "name: my-project\ninfra:\n  provider: bicep\n"},
		{name: "explicit default path", yaml: "name: my-project\ninfra:\n  path: infra\n"},
		{name: "explicit default path dot-prefixed", yaml: "name: my-project\ninfra:\n  path: ./infra\n"},
		{name: "explicit default path windows separators", yaml: "name: my-project\ninfra:\n  path: .\\infra\n"},
		{name: "empty path", yaml: "name: my-project\ninfra:\n  path: \"\"\n"},
		{
			name:     "custom path",
			yaml:     "name: my-project\ninfra:\n  path: myinfra\n",
			wantCode: exterrors.CodeInfraEjectCustomInfraPath,
		},
		{
			name:     "nested path",
			yaml:     "name: my-project\ninfra:\n  path: deploy/infra\n",
			wantCode: exterrors.CodeInfraEjectCustomInfraPath,
		},
		{
			name:     "space-padded path remains custom",
			yaml:     "name: my-project\ninfra:\n  path: \" infra \"\n",
			wantCode: exterrors.CodeInfraEjectCustomInfraPath,
		},
		{name: "malformed yaml", yaml: "name: [unterminated\n", wantCode: exterrors.CodeInvalidAzureYaml},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := ensureDefaultInfraPath([]byte(tt.yaml))
			if tt.wantCode == "" {
				assert.NoError(t, err)
				return
			}
			require.Error(t, err)
			localErr, ok := errors.AsType[*azdext.LocalError](err)
			require.True(t, ok, "expected *azdext.LocalError, got %T", err)
			assert.Equal(t, tt.wantCode, localErr.Code)
			if tt.wantCode == exterrors.CodeInfraEjectCustomInfraPath {
				assert.Contains(t, localErr.Suggestion, "without --infra",
					"the non-destructive path has to be offered")
			}
		})
	}
}

func TestEnsureDefaultInfraModule(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		yaml     string
		wantCode string
	}{
		{name: "no infra block", yaml: "name: my-project\n"},
		{name: "infra block without module", yaml: "name: my-project\ninfra:\n  provider: bicep\n"},
		{name: "explicit default module", yaml: "name: my-project\ninfra:\n  module: main\n"},
		{name: "dot-prefixed default module", yaml: "name: my-project\ninfra:\n  module: ./main\n"},
		{
			name:     "trailing separator is not the default module",
			yaml:     "name: my-project\ninfra:\n  module: main/\n",
			wantCode: exterrors.CodeInfraEjectCustomModule,
		},
		{
			name:     "trailing dot segment is not the default module",
			yaml:     "name: my-project\ninfra:\n  module: main/.\n",
			wantCode: exterrors.CodeInfraEjectCustomModule,
		},
		{
			name:     "custom module",
			yaml:     "name: my-project\ninfra:\n  module: platform\n",
			wantCode: exterrors.CodeInfraEjectCustomModule,
		},
		{
			name:     "type-invalid module",
			yaml:     "name: my-project\ninfra:\n  module: [main]\n",
			wantCode: exterrors.CodeInvalidAzureYaml,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := ensureDefaultInfraModule([]byte(tt.yaml))
			if tt.wantCode == "" {
				assert.NoError(t, err)
				return
			}

			require.Error(t, err)
			localErr, ok := errors.AsType[*azdext.LocalError](err)
			require.True(t, ok, "expected *azdext.LocalError, got %T", err)
			assert.Equal(t, tt.wantCode, localErr.Code)
			if tt.wantCode == exterrors.CodeInfraEjectCustomModule {
				assert.Contains(t, localErr.Message, "infra.module")
				assert.Contains(t, localErr.Suggestion, "without --infra")
			}
		})
	}
}

func TestEnsureNoInfraLayers(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		yaml     string
		wantCode string
	}{
		{name: "no infra block", yaml: "name: my-project\n"},
		{name: "empty layers", yaml: "name: my-project\ninfra:\n  layers: []\n"},
		{
			name: "layer with explicit provider",
			yaml: `name: my-project
infra:
  provider: terraform
  layers:
    - name: app
      provider: bicep
      path: infra/app
`,
			wantCode: exterrors.CodeInfraEjectLayersUnsupported,
		},
		{
			name: "layer inherits root provider",
			yaml: `name: my-project
infra:
  provider: terraform
  layers:
    - name: app
      path: infra/app
`,
			wantCode: exterrors.CodeInfraEjectLayersUnsupported,
		},
		{
			name:     "type-invalid layers",
			yaml:     "name: my-project\ninfra:\n  layers: unexpected\n",
			wantCode: exterrors.CodeInvalidAzureYaml,
		},
		{name: "malformed yaml", yaml: "name: [unterminated\n", wantCode: exterrors.CodeInvalidAzureYaml},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := ensureNoInfraLayers([]byte(tt.yaml))
			if tt.wantCode == "" {
				assert.NoError(t, err)
				return
			}

			require.Error(t, err)
			localErr, ok := errors.AsType[*azdext.LocalError](err)
			require.True(t, ok, "expected *azdext.LocalError, got %T", err)
			assert.Equal(t, tt.wantCode, localErr.Code)
			if tt.wantCode == exterrors.CodeInfraEjectLayersUnsupported {
				assert.Contains(t, localErr.Suggestion, "without --infra")
			}
		})
	}
}

func TestEnsureCompatibleInfraProvider(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		yaml      string
		requested string
		wantCode  string
	}{
		{name: "unspecified provider accepts bicep", yaml: "name: my-project\n", requested: "bicep"},
		{
			name:      "bicep provider accepts bicep",
			yaml:      "name: my-project\ninfra:\n  provider: bicep\n",
			requested: "bicep",
		},
		{
			name:      "foundry provider accepts bicep",
			yaml:      "name: my-project\ninfra:\n  provider: microsoft.foundry\n",
			requested: "bicep",
		},
		{
			name:      "terraform provider rejects bicep",
			yaml:      "name: my-project\ninfra:\n  provider: terraform\n",
			requested: "bicep",
			wantCode:  exterrors.CodeInfraEjectProviderConflict,
		},
		{
			name:      "terraform provider accepts terraform",
			yaml:      "name: my-project\ninfra:\n  provider: terraform\n",
			requested: "terraform",
		},
		{
			name:      "type-invalid provider",
			yaml:      "name: my-project\ninfra:\n  provider: [terraform]\n",
			requested: "terraform",
			wantCode:  exterrors.CodeInvalidAzureYaml,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := ensureCompatibleInfraProvider([]byte(tt.yaml), tt.requested)
			if tt.wantCode == "" {
				assert.NoError(t, err)
				return
			}

			require.Error(t, err)
			localErr, ok := errors.AsType[*azdext.LocalError](err)
			require.True(t, ok, "expected *azdext.LocalError, got %T", err)
			assert.Equal(t, tt.wantCode, localErr.Code)
			if tt.wantCode == exterrors.CodeInfraEjectProviderConflict {
				assert.Contains(t, localErr.Message, "ignore the generated files")
				assert.Contains(t, localErr.Suggestion, "--infra=terraform")
			}
		})
	}
}

func TestResolveInfraGate_AllowsCustomInfraPath(t *testing.T) {
	tests := []struct {
		name string
		yaml string
	}{
		{
			name: "project without a foundry service",
			yaml: `name: my-project
infra:
  path: myinfra
services:
  web:
    host: containerapp
`,
		},
		{
			name: "project that already declares a foundry service",
			yaml: `name: my-project
infra:
  path: myinfra
services:
  ai-project:
    host: azure.ai.project
`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("AZD_EXEC_PROJECT_DIR", "")
			projectRoot := t.TempDir()
			mustWriteFile(t, filepath.Join(projectRoot, "azure.yaml"), tt.yaml)
			// The declared directory exists; ./infra/ deliberately does not, so
			// only the infra.path check can catch this.
			require.NoError(t, os.MkdirAll(filepath.Join(projectRoot, "myinfra"), 0o750))
			t.Chdir(projectRoot)

			gate, err := resolveInfraGate("bicep")
			require.NoError(t, err)
			assert.Equal(t, tt.name == "project that already declares a foundry service", gate.standaloneEject)
		})
	}
}

func TestEjectInfra_Terraform_HonorsCustomInfraPath(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	azureYAML := `name: my-project
infra:
  path: myinfra
services:
  my-foundry:
    host: azure.ai.project
`
	mustWriteFile(t, filepath.Join(dir, "azure.yaml"), azureYAML)

	require.NoError(t, ejectInfra(dir, "terraform"))
	assert.FileExists(t, filepath.Join(dir, "myinfra", "main.tf"))
	assert.FileExists(t, filepath.Join(dir, "myinfra", "main.tfvars.json"))
}

func TestResolveInfraGate_AllowsInfraLayers(t *testing.T) {
	tests := []struct {
		name string
		host string
	}{
		{name: "init fall-through", host: "containerapp"},
		{name: "standalone eject", host: "azure.ai.project"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("AZD_EXEC_PROJECT_DIR", "")
			projectRoot := t.TempDir()
			mustWriteFile(t, filepath.Join(projectRoot, "azure.yaml"), fmt.Sprintf(`name: my-project
infra:
  provider: terraform
  layers:
    - name: existing
      path: infra/existing
services:
  service:
    host: %s
`, tt.host))
			t.Chdir(projectRoot)

			gate, err := resolveInfraGate("terraform")
			require.NoError(t, err)
			assert.Equal(t, tt.host == "azure.ai.project", gate.standaloneEject)
		})
	}
}

func TestResolveInfraGate_DefersProviderCompatibility(t *testing.T) {
	tests := []struct {
		name string
		host string
	}{
		{name: "init fall-through", host: "containerapp"},
		{name: "standalone eject", host: "azure.ai.project"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("AZD_EXEC_PROJECT_DIR", "")
			projectRoot := t.TempDir()
			mustWriteFile(t, filepath.Join(projectRoot, "azure.yaml"), fmt.Sprintf(`name: my-project
infra:
  provider: terraform
services:
  service:
    host: %s
`, tt.host))
			t.Chdir(projectRoot)

			gate, err := resolveInfraGate("bicep")
			require.NoError(t, err)
			assert.Equal(t, tt.host == "azure.ai.project", gate.standaloneEject)
		})
	}
}

func TestResolveInfraGate_AllowsCustomInfraModule(t *testing.T) {
	t.Setenv("AZD_EXEC_PROJECT_DIR", "")
	projectRoot := t.TempDir()
	mustWriteFile(t, filepath.Join(projectRoot, "azure.yaml"), `name: my-project
infra:
  module: platform
services:
  service:
    host: containerapp
`)
	t.Chdir(projectRoot)

	gate, err := resolveInfraGate("bicep")
	require.NoError(t, err)
	assert.False(t, gate.standaloneEject)
}

func TestEjectInfra_RefusesOnlyFoundryLayer(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	azureYAML := `name: my-project
infra:
  provider: terraform
  layers:
    - name: foundry
      path: infra/foundry
services:
  my-foundry:
    host: azure.ai.project
`
	mustWriteFile(t, filepath.Join(dir, "azure.yaml"), azureYAML)

	err := ejectInfra(dir, "terraform")
	require.Error(t, err)
	localErr, ok := errors.AsType[*azdext.LocalError](err)
	require.True(t, ok, "expected *azdext.LocalError, got %T", err)
	assert.Equal(t, exterrors.CodeInvalidAzureYaml, localErr.Code)
	assert.NoDirExists(t, filepath.Join(dir, "infra"))

	raw, readErr := os.ReadFile(filepath.Join(dir, "azure.yaml")) //nolint:gosec // G304: test path from t.TempDir()
	require.NoError(t, readErr)
	assert.Equal(t, azureYAML, string(raw), "a refused eject must not rewrite the provider or layers")
}

func TestEjectInfra_HonorsCustomInfraModule(t *testing.T) {
	t.Parallel()
	for _, provider := range []string{"bicep", "terraform"} {
		t.Run(provider, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			azureYAML := `name: my-project
infra:
  module: platform
services:
  my-foundry:
    host: azure.ai.project
`
			mustWriteFile(t, filepath.Join(dir, "azure.yaml"), azureYAML)

			require.NoError(t, ejectInfra(dir, provider))
			if provider == "bicep" {
				assert.FileExists(t, filepath.Join(dir, "infra", "platform.bicep"))
				assert.FileExists(t, filepath.Join(dir, "infra", "platform.parameters.json"))
			} else {
				assert.FileExists(t, filepath.Join(dir, "infra", "platform.tfvars.json"))
			}
		})
	}
}

func TestEjectInfra_BicepRefusesTerraformProvider(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	azureYAML := `name: my-project
infra:
  provider: terraform
services:
  my-foundry:
    host: azure.ai.project
`
	mustWriteFile(t, filepath.Join(dir, "azure.yaml"), azureYAML)

	err := ejectInfra(dir, "bicep")
	require.Error(t, err)
	localErr, ok := errors.AsType[*azdext.LocalError](err)
	require.True(t, ok, "expected *azdext.LocalError, got %T", err)
	assert.Equal(t, exterrors.CodeInvalidAzureYaml, localErr.Code)
	assert.NoDirExists(t, filepath.Join(dir, "infra"))
}

// TestResolveInfraGate_ExistingProjectWithoutFoundryServiceRunsInit is the
// regression test for #9124: `azd ai agent init --infra` inside an azd project
// that has no Foundry service must fall through to the normal init flow rather
// than refusing with CodeInfraEjectNoFoundryService ("nothing to eject").
func TestResolveInfraGate_ExistingProjectWithoutFoundryServiceRunsInit(t *testing.T) {
	t.Setenv("AZD_EXEC_PROJECT_DIR", "")
	projectRoot := t.TempDir()
	mustWriteFile(t, filepath.Join(projectRoot, "azure.yaml"), `name: my-project
services:
  web:
    host: containerapp
    project: src/web
`)
	t.Chdir(projectRoot)

	gate, err := resolveInfraGate("bicep")
	require.NoError(t, err, "an existing project without a Foundry service must not refuse")
	assert.False(t, gate.standaloneEject, "init runs first, then the trailing eject")
	assert.Equal(t, projectRoot, gate.projectRoot)
}

func TestResolveInfraGate_ExistingFoundryProjectEjectsStandalone(t *testing.T) {
	t.Setenv("AZD_EXEC_PROJECT_DIR", "")
	projectRoot := t.TempDir()
	mustWriteFile(t, filepath.Join(projectRoot, "azure.yaml"), validFoundryAzureYAML)
	t.Chdir(projectRoot)

	gate, err := resolveInfraGate("bicep")
	require.NoError(t, err)
	assert.True(t, gate.standaloneEject)
	assert.Equal(t, projectRoot, gate.projectRoot)
}

func TestResolveInfraGate_NoProjectRunsInit(t *testing.T) {
	t.Setenv("AZD_EXEC_PROJECT_DIR", "")
	t.Chdir(t.TempDir())

	gate, err := resolveInfraGate("bicep")
	require.NoError(t, err)
	assert.False(t, gate.standaloneEject)
	assert.Empty(t, gate.projectRoot, "no project root to eject from yet")
}

func TestResolveInfraGate_AllowsExistingInfraBeforeRunningInit(t *testing.T) {
	t.Setenv("AZD_EXEC_PROJECT_DIR", "")
	projectRoot := t.TempDir()
	mustWriteFile(t, filepath.Join(projectRoot, "azure.yaml"), `name: my-project
services:
  web:
    host: containerapp
`)
	require.NoError(t, os.MkdirAll(filepath.Join(projectRoot, "infra"), 0o750))
	t.Chdir(projectRoot)

	gate, err := resolveInfraGate("bicep")
	require.NoError(t, err)
	assert.False(t, gate.standaloneEject)
	assert.Equal(t, projectRoot, gate.projectRoot)
}

func TestResolveInfraGate_PropagatesInvalidFoundryConfiguration(t *testing.T) {
	t.Setenv("AZD_EXEC_PROJECT_DIR", "")
	projectRoot := t.TempDir()
	mustWriteFile(t, filepath.Join(projectRoot, "azure.yaml"), `name: my-project
services:
  first:
    host: azure.ai.project
  second:
    host: azure.ai.project
`)
	t.Chdir(projectRoot)

	_, err := resolveInfraGate("bicep")
	require.Error(t, err)
	localErr, ok := errors.AsType[*azdext.LocalError](err)
	require.True(t, ok, "expected *azdext.LocalError, got %T", err)
	assert.Equal(t, exterrors.CodeInfraEjectMultipleFoundryServices, localErr.Code)
}

// End-to-end through cobra with nothing in the way: before #9124 this returned
// CodeInfraEjectNoFoundryService before the command did anything else. The gate
// now falls through, so the run reaches the normal init flow and only stops
// there, on the azd host RPCs this test deliberately leaves unreachable.
//
// The interesting assertions are negative — no eject-gate refusal fires, and a
// refused-at-the-gate run cannot be mistaken for a fall-through because it never
// reaches the azd host at all.
func TestInitInfra_ExistingProjectWithoutFoundryServiceFallsThroughToInit(t *testing.T) {
	t.Setenv("AZD_EXEC_PROJECT_DIR", "")
	// No azd host, and no `azd` on PATH for the auth probe, so the run fails at
	// a fixed point inside init instead of depending on the developer's machine.
	t.Setenv("AZD_SERVER", "")
	t.Setenv("PATH", "")
	projectRoot := t.TempDir()
	azureYAML := `name: my-project
services:
  web:
    host: containerapp
`
	mustWriteFile(t, filepath.Join(projectRoot, "azure.yaml"), azureYAML)
	t.Chdir(projectRoot)

	cmd := newInitCommand(&azdext.ExtensionContext{})
	cmd.SetArgs([]string{"--infra"})
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)

	var execErr error
	withCapturedStdout(t, func() {
		execErr = cmd.Execute()
	})

	require.Error(t, execErr, "init cannot complete without an azd host")
	if localErr, ok := errors.AsType[*azdext.LocalError](execErr); ok {
		assert.NotContains(t, []string{
			exterrors.CodeInfraEjectNoFoundryService,
			exterrors.CodeInfraEjectExists,
			exterrors.CodeInfraEjectCustomInfraPath,
			exterrors.CodeInfraEjectConflictingArguments,
		}, localErr.Code, "--infra must not refuse a project that simply has no agent service yet")
	}
	assert.Contains(t, execErr.Error(), "rpc error",
		"the run has to get far enough into init to talk to the azd host")

	// A refusal at the gate is the only thing that could have stopped the run
	// earlier, and it would have left these untouched too — so also assert init
	// did not half-write anything on its way out.
	assert.NoDirExists(t, filepath.Join(projectRoot, "infra"))
	raw, err := os.ReadFile(filepath.Join(projectRoot, "azure.yaml")) //nolint:gosec // G304: test path from t.TempDir()
	require.NoError(t, err)
	assert.Equal(t, azureYAML, string(raw))
}

// The fall-through only pays off if the trailing eject still runs: every init
// exit path ends in ejectInfraAfterInit. Drive that seam end to end — gate,
// then the azure.yaml mutation init performs when it adds the agent service,
// then the trailing eject — so a regression that stopped generating IaC after
// init on an existing project cannot pass.
//
// The init flow in between is exercised by its own tests; reproducing it here
// would mean standing up the whole azd host (prompts, model catalog, template
// download), which belongs in the functional suite.
func TestInitInfra_FallThroughEjectsAfterInitAddsFoundryService(t *testing.T) {
	t.Setenv("AZD_EXEC_PROJECT_DIR", "")
	projectRoot := t.TempDir()
	mustWriteFile(t, filepath.Join(projectRoot, "azure.yaml"), `name: my-project
services:
  web:
    host: containerapp
`)
	t.Chdir(projectRoot)

	gate, err := resolveInfraGate("bicep")
	require.NoError(t, err)
	require.False(t, gate.standaloneEject, "no Foundry service yet, so init has to run first")
	require.Equal(t, projectRoot, gate.projectRoot)

	// Stand in for the init flow: add the Foundry project service the same way
	// init does before it hands off to ejectInfraAfterInit.
	mustWriteFile(t, filepath.Join(projectRoot, "azure.yaml"), `name: my-project
services:
  web:
    host: containerapp
  ai-project:
    host: azure.ai.project
    deployments:
      - name: gpt-4-1-mini
        model:
          name: gpt-4.1-mini
          format: OpenAI
          version: "2024-07-18"
        sku:
          name: GlobalStandard
          capacity: 50
`)

	withCapturedStdout(t, func() {
		require.NoError(t, ejectInfraAfterInit("bicep"))
	})

	assert.FileExists(t, filepath.Join(projectRoot, "infra", "main.bicep"))
	assert.FileExists(t, filepath.Join(projectRoot, "infra", "main.parameters.json"))

	// The project's pre-existing service has to survive the round trip.
	raw, err := os.ReadFile(filepath.Join(projectRoot, "azure.yaml")) //nolint:gosec // G304: test path from t.TempDir()
	require.NoError(t, err)
	assert.Contains(t, string(raw), "host: containerapp")
	assert.Contains(t, string(raw), "host: azure.ai.project")
}

func TestEjectInfra_Terraform_HappyPath_WritesExpectedFiles(t *testing.T) {
	// Not parallel: captures os.Stdout (see TestEjectInfra_HappyPath_WritesExpectedFiles).
	dir := t.TempDir()
	mustWriteFile(t, filepath.Join(dir, "azure.yaml"), validFoundryAzureYAML)

	stdout := withCapturedStdout(t, func() {
		require.NoError(t, ejectInfra(dir, "terraform"))
	})

	// Every embedded .tf under templates/terraform/ should be on disk under
	// ./infra/, plus the generated marker and main.tfvars.json. Bicep artifacts must NOT
	// be present.
	expected := []string{
		filepath.Join("infra", "provider.tf"),
		filepath.Join("infra", "variables.tf"),
		filepath.Join("infra", "main.tf"),
		filepath.Join("infra", "acr.tf"),
		filepath.Join("infra", "connections.tf"),
		filepath.Join("infra", "outputs.tf"),
		filepath.Join("infra", "main.tfvars.json"),
		filepath.Join("infra", foundryTerraformMarker),
	}
	for _, rel := range expected {
		info, err := os.Stat(filepath.Join(dir, rel))
		require.NoError(t, err, "expected file %s", rel)
		assert.Greater(t, info.Size(), int64(0), "file %s should not be empty", rel)
	}

	// Bicep outputs must not leak onto the Terraform path.
	for _, rel := range []string{
		filepath.Join("infra", "main.bicep"),
		filepath.Join("infra", "main.parameters.json"),
		filepath.Join("infra", "modules", "acr.bicep"),
	} {
		_, err := os.Stat(filepath.Join(dir, rel))
		assert.True(t, os.IsNotExist(err), "%s must not be written on the terraform path", rel)
	}

	// Summary mentions the created files and the azure.yaml provider stamp.
	assert.Contains(t, stdout, "Generating infrastructure files from azure.yaml")
	assert.Contains(t, stdout, "infra/main.tf")
	assert.Contains(t, stdout, "infra/main.tfvars.json")
	assert.Contains(t, stdout, "infra.provider: terraform")
	assert.Contains(t, stdout, "azd provision")

	rawYAML, err := os.ReadFile(filepath.Join(dir, "azure.yaml")) //nolint:gosec // G304: test path from t.TempDir()
	require.NoError(t, err)
	var projectDoc struct {
		Infra struct {
			Provider string `yaml:"provider"`
			Layers   []any  `yaml:"layers"`
		} `yaml:"infra"`
	}
	require.NoError(t, yaml.Unmarshal(rawYAML, &projectDoc))
	assert.Equal(t, "terraform", projectDoc.Infra.Provider)
	assert.Empty(t, projectDoc.Infra.Layers)

	// This fixture has a docker: agent, so acr.tf is present and the generated
	// outputs.tf must reference the registry resources (not empty strings).
	outputs, err := os.ReadFile(filepath.Join(dir, "infra", "outputs.tf")) //nolint:gosec // G304: test path from t.TempDir()
	require.NoError(t, err)
	assert.Contains(t, string(outputs), "azurerm_container_registry.this.login_server",
		"docker fixture => ACR outputs reference the registry")
}

func TestEjectInfra_Terraform_StampsProviderInAzureYaml(t *testing.T) {
	// Not parallel: captures os.Stdout.
	dir := t.TempDir()
	mustWriteFile(t, filepath.Join(dir, "azure.yaml"), validFoundryAzureYAML)

	withCapturedStdout(t, func() {
		require.NoError(t, ejectInfra(dir, "terraform"))
	})

	raw, err := os.ReadFile(filepath.Join(dir, "azure.yaml")) //nolint:gosec // G304: test file path from t.TempDir()
	require.NoError(t, err)

	var doc struct {
		Infra struct {
			Provider string `yaml:"provider"`
			Path     string `yaml:"path"`
		} `yaml:"infra"`
		Services map[string]any `yaml:"services"`
	}
	require.NoError(t, yaml.Unmarshal(raw, &doc))

	// The Terraform path is the one place eject mutates azure.yaml: the
	// provider must flip from microsoft.foundry to terraform so azd-core's
	// built-in provider handles provisioning.
	assert.Equal(t, "terraform", doc.Infra.Provider)
	assert.Empty(t, doc.Infra.Path, "starter infra.path must be dropped")
	// The rest of azure.yaml (services) must survive the edit.
	require.Contains(t, doc.Services, "my-foundry")
}

func TestEjectInfra_Terraform_TfvarsShape(t *testing.T) {
	// Not parallel: captures os.Stdout.
	dir := t.TempDir()
	mustWriteFile(t, filepath.Join(dir, "azure.yaml"), validFoundryAzureYAML)

	withCapturedStdout(t, func() {
		require.NoError(t, ejectInfra(dir, "terraform"))
	})

	raw, err := os.ReadFile(filepath.Join(dir, "infra", "main.tfvars.json")) //nolint:gosec // G304: test file path from t.TempDir()
	require.NoError(t, err)

	var doc map[string]any
	require.NoError(t, json.Unmarshal(raw, &doc), "main.tfvars.json must be valid JSON")

	// Static keys carry ${...} placeholders azd resolves at provision time.
	assert.Equal(t, "${AZURE_LOCATION}", doc["location"])
	assert.Equal(t, "${AZURE_RESOURCE_GROUP}", doc["resource_group_name"])
	assert.Equal(t, "${AZURE_AI_PROJECT_NAME}", doc["foundry_project_name"])
	assert.Equal(t, "${AZURE_SUBSCRIPTION_ID}", doc["subscription_id"])
	assert.Equal(t, "${AZURE_PRINCIPAL_ID}", doc["principal_id"])
	assert.NotContains(t, doc, "create_resource_group")

	// include_acr is NOT written to tfvars; the ACR decision is the presence of
	// acr.tf at eject time, not a Terraform variable.
	assert.NotContains(t, doc, "include_acr",
		"include_acr must not be emitted to main.tfvars.json")

	// deployments is the synthesizer-derived value carried into tfvars.
	deps, ok := doc["deployments"].([]any)
	require.True(t, ok, "deployments should be an array, got %T", doc["deployments"])
	require.Len(t, deps, 1)

	// connections is always present too (empty here: the fixture declares
	// none), so a project with no host: azure.ai.connection services still
	// gets a well-typed empty list rather than a missing key.
	conns, ok := doc["connections"].([]any)
	require.True(t, ok, "connections should be an array, got %T", doc["connections"])
	assert.Empty(t, conns)
	assert.NotContains(t, doc, "connectionCredentials")
}

func TestEjectInfra_Terraform_EjectsConnectionServices(t *testing.T) {
	// Not parallel: captures os.Stdout.
	// A host: azure.ai.connection service must be synthesized into the
	// connections tfvars value, connections.tf must be part of the ejected
	// tree, and any ${VAR} in credentials kept verbatim (environment-portable
	// -- azd's Terraform provider substitutes ${...} at provision time).
	dir := t.TempDir()
	mustWriteFile(t, filepath.Join(dir, "azure.yaml"), `name: my-project
services:
  my-foundry:
    host: azure.ai.project
    deployments: []
  search-conn:
    host: azure.ai.connection
    uses: [my-foundry]
    category: CognitiveSearch
    target: https://my-search.search.windows.net
    authType: ApiKey
    credentials:
      key: ${SEARCH_API_KEY}
`)

	withCapturedStdout(t, func() {
		require.NoError(t, ejectInfra(dir, "terraform"))
	})

	// connections.tf is part of the ejected tree.
	_, err := os.Stat(filepath.Join(dir, "infra", "connections.tf"))
	assert.NoError(t, err, "connections.tf must be ejected")

	raw, err := os.ReadFile(filepath.Join(dir, "infra", "main.tfvars.json")) //nolint:gosec // G304: test file path from t.TempDir()
	require.NoError(t, err)
	var doc map[string]any
	require.NoError(t, json.Unmarshal(raw, &doc))

	conns, ok := doc["connections"].([]any)
	require.True(t, ok, "connections should be an array, got %T", doc["connections"])
	require.Len(t, conns, 1)

	conn, ok := conns[0].(map[string]any)
	require.True(t, ok, "connection entry should be an object, got %T", conns[0])
	assert.Equal(t, "search-conn", conn["name"])
	assert.Equal(t, "CognitiveSearch", conn["category"])
	assert.Equal(t, "ApiKey", conn["authType"])

	// ${VAR} in credentials must be preserved verbatim on the eject path.
	creds, ok := conn["credentials"].(map[string]any)
	require.True(t, ok, "credentials should be an object, got %T", conn["credentials"])
	assert.Equal(t, "${SEARCH_API_KEY}", creds["key"])
	assert.NotContains(t, doc, "connectionCredentials")

	// outputs.tf always carries the connection-names output, unconditional on
	// includeAcr (unlike the ACR outputs).
	outputs, err := os.ReadFile(filepath.Join(dir, "infra", "outputs.tf")) //nolint:gosec // G304: test path from t.TempDir()
	require.NoError(t, err)
	assert.Contains(t, string(outputs), "AZURE_AI_PROJECT_CONNECTION_NAMES")
	assert.Contains(t, string(outputs), "AZURE_AI_PROJECT_CONNECTIONS_PROJECT_ENDPOINT")
}

func TestEjectInfra_Terraform_NoDockerOmitsAcr(t *testing.T) {
	// Not parallel: captures os.Stdout.
	dir := t.TempDir()
	// image-only agent (no docker:) -> no ACR.
	mustWriteFile(t, filepath.Join(dir, "azure.yaml"), `name: my-project
services:
  my-foundry:
    host: azure.ai.project
    deployments: []
    agents:
      - name: my-agent
        image: registry.io/myorg/myagent:latest
`)

	withCapturedStdout(t, func() {
		require.NoError(t, ejectInfra(dir, "terraform"))
	})

	// acr.tf must NOT be written when no agent uses docker:.
	_, err := os.Stat(filepath.Join(dir, "infra", "acr.tf"))
	assert.True(t, os.IsNotExist(err), "acr.tf must be omitted when no agent uses docker:")

	// outputs.tf must not contain any ACR output at all when ACR is not used --
	// no resource references and no empty-string placeholders.
	outputs, err := os.ReadFile(filepath.Join(dir, "infra", "outputs.tf")) //nolint:gosec // G304: test path from t.TempDir()
	require.NoError(t, err)
	assert.NotContains(t, string(outputs), "azurerm_container_registry",
		"no ACR resource references when acr.tf is omitted")
	assert.NotContains(t, string(outputs), "azapi_resource.acr_connection",
		"no ACR connection reference when acr.tf is omitted")
	assert.NotContains(t, string(outputs), "AZURE_CONTAINER_REGISTRY_ENDPOINT",
		"ACR outputs must be omitted entirely, not emitted as empty strings")
	assert.NotContains(t, string(outputs), "AZURE_CONTAINER_REGISTRY_RESOURCE_ID")
	assert.NotContains(t, string(outputs), "AZURE_AI_PROJECT_ACR_CONNECTION_NAME")
	// The non-ACR outputs are still present.
	assert.Contains(t, string(outputs), "AZURE_FOUNDRY_RESOURCE_GROUP")
	assert.Contains(t, string(outputs), "FOUNDRY_PROJECT_ENDPOINT")

	// main.tf must not carry any ACR leftovers (e.g. container_registry_name).
	main, err := os.ReadFile(filepath.Join(dir, "infra", "main.tf")) //nolint:gosec // G304: test path from t.TempDir()
	require.NoError(t, err)
	assert.NotContains(t, string(main), "container_registry",
		"main.tf must have no ACR references when ACR is not used")

	// include_acr is not emitted to tfvars either.
	raw, err := os.ReadFile(filepath.Join(dir, "infra", "main.tfvars.json")) //nolint:gosec // G304: test file path from t.TempDir()
	require.NoError(t, err)
	var doc map[string]any
	require.NoError(t, json.Unmarshal(raw, &doc))
	assert.NotContains(t, doc, "include_acr")
}

func TestEjectInfra_Terraform_MigratesExistingInfraToLayers(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	mustWriteFile(t, filepath.Join(dir, "azure.yaml"),
		strings.Replace(validFoundryAzureYAML, "provider: microsoft.foundry", "provider: bicep", 1))
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "infra"), 0o750))
	mustWriteFile(t, filepath.Join(dir, "infra", "main.bicep"), "// existing infrastructure\n")

	err := ejectInfra(dir, "terraform")
	require.NoError(t, err)
	assert.FileExists(t, filepath.Join(dir, "infra", "foundry", "main.tf"))
	tfvars, err := os.ReadFile(filepath.Join(dir, "infra", "foundry", "main.tfvars.json")) //nolint:gosec
	require.NoError(t, err)
	var tfvarsDoc map[string]any
	require.NoError(t, json.Unmarshal(tfvars, &tfvarsDoc))
	assert.NotContains(t, tfvarsDoc, "create_resource_group")
	assert.Equal(t, "${AZURE_FOUNDRY_RESOURCE_GROUP=rg-${AZURE_ENV_NAME}-foundry}",
		tfvarsDoc["resource_group_name"])
	assert.Equal(t, "${AZURE_AI_PROJECT_NAME=}", tfvarsDoc["foundry_project_name"])
	assert.Equal(t, "${AZD_RESOURCE_TOKEN_SALT}", tfvarsDoc["resource_token_salt"])

	raw, err := os.ReadFile(filepath.Join(dir, "azure.yaml")) //nolint:gosec // G304: test file path from t.TempDir()
	require.NoError(t, err)
	assert.Contains(t, string(raw), "path: infra/foundry")
	assert.Contains(t, string(raw), "provider: terraform")
}

func TestEjectInfra_Terraform_RefusesWhenNetworkDeclared(t *testing.T) {
	t.Parallel()
	// Private networking is Bicep-only: the Terraform module has no VNet / PE /
	// DNS / networkInjections resources. Ejecting it for a network: service would
	// silently drop the config and provision a public account. Eject must refuse
	// rather than emit an insecure template.
	dir := t.TempDir()
	mustWriteFile(t, filepath.Join(dir, "azure.yaml"), `name: my-project
infra:
  provider: microsoft.foundry
services:
  my-foundry:
    host: azure.ai.project
    network:
      peSubnet: {vnet: "${AZURE_VNET_ID}", name: pe-subnet}
    deployments: []
    agents:
      - name: my-agent
        image: registry.io/myorg/myagent:latest
`)

	err := ejectInfra(dir, "terraform")
	require.Error(t, err)
	localErr, ok := errors.AsType[*azdext.LocalError](err)
	require.True(t, ok, "expected structured azdext.LocalError, got %T", err)
	assert.Equal(t, exterrors.CodeInfraEjectNetworkUnsupported, localErr.Code)

	// The refusal must fire before any files land or azure.yaml is stamped.
	_, statErr := os.Stat(filepath.Join(dir, "infra"))
	assert.True(t, os.IsNotExist(statErr), "infra/ must not be written when eject refuses")
	raw, err := os.ReadFile(filepath.Join(dir, "azure.yaml")) //nolint:gosec // G304: test file path from t.TempDir()
	require.NoError(t, err)
	assert.Contains(t, string(raw), "provider: microsoft.foundry",
		"azure.yaml must not be stamped when eject refuses")
}
