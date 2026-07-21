// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package provisioning

import (
	"testing"

	"github.com/braydonk/yaml"
	"github.com/stretchr/testify/require"
)

func TestDeploymentStacksConfigUnmarshalYAML(t *testing.T) {
	const source = `
provider: bicep
deploymentStacks:
  actionOnUnmanage:
    resources: delete
    resourceGroups: detach
  denySettings:
    mode: denyDelete
    applyToChildScopes: true
    excludedActions:
      - Microsoft.Authorization/*/write
    excludedPrincipals:
      - ${PIPELINE_SP_OBJECT_ID}
      - literal-principal-id
`

	var options Options
	require.NoError(t, yaml.Unmarshal([]byte(source), &options))

	require.NotNil(t, options.DeploymentStacks)
	require.NotNil(t, options.DeploymentStacks.ActionOnUnmanage)
	require.Equal(t, "delete", options.DeploymentStacks.ActionOnUnmanage.Resources)
	require.Equal(t, "detach", options.DeploymentStacks.ActionOnUnmanage.ResourceGroups)

	denySettings := options.DeploymentStacks.DenySettings
	require.NotNil(t, denySettings)
	require.Equal(t, "denyDelete", denySettings.Mode)
	require.NotNil(t, denySettings.ApplyToChildScopes)
	require.True(t, *denySettings.ApplyToChildScopes)

	require.Len(t, denySettings.ExcludedActions, 1)
	action, err := denySettings.ExcludedActions[0].Envsubst(func(string) string { return "" })
	require.NoError(t, err)
	require.Equal(t, "Microsoft.Authorization/*/write", action)

	require.Len(t, denySettings.ExcludedPrincipals, 2)
	resolved, err := denySettings.ExcludedPrincipals[0].Envsubst(func(name string) string {
		if name == "PIPELINE_SP_OBJECT_ID" {
			return "resolved-sp-id"
		}
		return ""
	})
	require.NoError(t, err)
	require.Equal(t, "resolved-sp-id", resolved)

	literal, err := denySettings.ExcludedPrincipals[1].Envsubst(func(string) string { return "" })
	require.NoError(t, err)
	require.Equal(t, "literal-principal-id", literal)
}
