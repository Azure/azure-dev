// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package provisioning

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const agentsProjectPath = "../../../azure.ai.agents/internal/project"

func TestAgentsProvisioningConstantsMatch(t *testing.T) {
	t.Parallel()

	agents := readStringConstants(
		t,
		filepath.Join(
			agentsProjectPath,
			"provisioning_provider.go",
		),
	)

	assert.Equal(t, FoundryProviderName, agents["FoundryProviderName"])
	assert.Equal(t, FoundryProjectHost, agents["FoundryProjectHost"])
	assert.Equal(t, BicepProviderName, agents["BicepProviderName"])
	assert.Equal(
		t,
		TerraformProviderName,
		agents["TerraformProviderName"],
	)
	assert.Equal(
		t,
		[]string{"azure.ai.project"},
		FoundryProjectServiceHosts,
	)
	assert.Equal(
		t,
		[]string{"azure.ai.agent", "microsoft.foundry"},
		FoundryLegacyProvisioningHosts,
	)
}

func TestAgentsProvisioningErrorCodesMatch(t *testing.T) {
	t.Parallel()

	projects := readStringConstants(
		t,
		filepath.Join("..", "exterrors", "codes.go"),
	)
	agents := readStringConstants(
		t,
		filepath.Join(
			agentsProjectPath,
			"..",
			"exterrors",
			"codes.go",
		),
	)

	names := []string{
		"CodeArmWhatIfFailed",
		"CodeAzdClientFailed",
		"CodeCredentialCreationFailed",
		"CodeDestroyRequiresForce",
		"CodeEnvironmentNotFound",
		"CodeEnvironmentValuesFailed",
		"CodeInvalidAzureYaml",
		"CodeInvalidServiceConfig",
		"CodeMissingAzureLocation",
		"CodeMissingAzureSubscription",
		"CodeMissingResourceGroup",
		"CodeOnDiskBicepCompileFailed",
		"CodeOnDiskBicepParseFailed",
		"CodeOnDiskParametersInvalid",
		"CodeOnDiskTemplateMissing",
		"CodeProvisioningServiceNotFound",
		"CodeTenantLookupFailed",
		"OpArmDeploymentCreate",
		"OpArmDeploymentGet",
		"OpArmDeploymentWhatIf",
		"OpCognitiveAccountList",
		"OpCognitiveAccountPurge",
		"OpCognitiveDeploymentDelete",
		"OpCognitiveDeploymentList",
		"OpResourceGroupDelete",
	}
	for _, name := range names {
		assert.NotEmpty(t, projects[name], name)
		assert.Equal(t, projects[name], agents[name], name)
	}
}

func readStringConstants(
	t *testing.T,
	path string,
) map[string]string {
	t.Helper()

	file, err := parser.ParseFile(
		token.NewFileSet(),
		path,
		nil,
		0,
	)
	require.NoError(t, err)

	values := map[string]string{}
	ast.Inspect(file, func(node ast.Node) bool {
		spec, ok := node.(*ast.ValueSpec)
		if !ok {
			return true
		}
		for i, name := range spec.Names {
			if i >= len(spec.Values) {
				continue
			}
			literal, ok := spec.Values[i].(*ast.BasicLit)
			if !ok || literal.Kind != token.STRING {
				continue
			}
			value, err := strconv.Unquote(literal.Value)
			require.NoError(t, err)
			values[name.Name] = value
		}
		return true
	})
	return values
}
