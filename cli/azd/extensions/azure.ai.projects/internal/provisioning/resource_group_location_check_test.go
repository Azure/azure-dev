// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package provisioning

import (
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEvaluateResourceGroupLocation(t *testing.T) {
	const (
		resourceGroup  = "rg-myenv-abc123"
		subscriptionID = "00000000-0000-0000-0000-000000000000"
	)

	t.Run("matching regions produce no results", func(t *testing.T) {
		resp := evaluateResourceGroupLocation(resourceGroup, "eastus", "eastus", subscriptionID)
		require.NotNil(t, resp)
		assert.Empty(t, resp.Results)
	})

	t.Run("region comparison is case-insensitive", func(t *testing.T) {
		resp := evaluateResourceGroupLocation(resourceGroup, "EastUS", "eastus", subscriptionID)
		require.NotNil(t, resp)
		assert.Empty(t, resp.Results)
	})

	t.Run("mismatched regions produce a blocking error result with guidance", func(t *testing.T) {
		resp := evaluateResourceGroupLocation(resourceGroup, "eastus", "westus2", subscriptionID)
		require.NotNil(t, resp)
		require.Len(t, resp.Results, 1)

		result := resp.Results[0]
		assert.Equal(t,
			azdext.ValidationCheckSeverity_VALIDATION_CHECK_SEVERITY_ERROR,
			result.Severity,
		)
		assert.Equal(t, ResourceGroupLocationRuleID, result.DiagnosticId)

		// The message should surface the immutable-region ARM failure and both regions.
		assert.Contains(t, result.Message, resourceGroup)
		assert.Contains(t, result.Message, "eastus")
		assert.Contains(t, result.Message, "westus2")
		assert.Contains(t, result.Message, "InvalidResourceGroupLocation")

		// The suggestion should offer all three remediation paths.
		assert.Contains(t, result.Suggestion, "azd env set AZURE_LOCATION eastus")
		assert.Contains(t, result.Suggestion, "azd env set AZURE_RESOURCE_GROUP")
		// The resource-group name is shell-quoted (%q) so a name containing shell
		// metacharacters (parentheses are valid in Azure RG names) stays copy-pasteable.
		assert.Contains(t, result.Suggestion, `az group delete --name "`+resourceGroup+`"`)
		assert.Contains(t, result.Suggestion, subscriptionID)
	})

	t.Run("resource group name with shell metacharacters is quoted in the delete command", func(t *testing.T) {
		const parenRG = "rg(test)"
		resp := evaluateResourceGroupLocation(parenRG, "eastus", "westus2", subscriptionID)
		require.NotNil(t, resp)
		require.Len(t, resp.Results, 1)

		// Without quoting, "az group delete --name rg(test)" is a shell syntax error.
		assert.Contains(t, resp.Results[0].Suggestion, `az group delete --name "rg(test)"`)
	})
}
