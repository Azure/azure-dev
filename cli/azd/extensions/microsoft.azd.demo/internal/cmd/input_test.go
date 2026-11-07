// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"testing"

	v1beta "github.com/azure/azure-dev/cli/azd/pkg/azdext/contracts/v1beta"
	"github.com/azure/azure-dev/cli/azd/pkg/inputs"
	"github.com/stretchr/testify/require"
)

func TestDemoInputProvider(t *testing.T) {
	for _, tt := range []struct {
		name       string
		definition *v1beta.InputDefinition
		want       *string
	}{
		{name: "environment", definition: &v1beta.InputDefinition{Kind: inputs.EnvironmentNameKind}, want: new("demo")},
		{name: "location", definition: &v1beta.InputDefinition{Kind: inputs.AzureLocationKind}, want: new("eastus2")},
		{name: "allowed location", definition: &v1beta.InputDefinition{
			Kind: inputs.AzureLocationKind, AllowedValues: []string{"westus3", "eastus2"},
		}, want: new("westus3")},
		{name: "unknown", definition: &v1beta.InputDefinition{Kind: "unknown/v1"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			value, err := resolveDemoInput(t.Context(), tt.definition)
			require.NoError(t, err)
			if tt.want == nil {
				require.Nil(t, value)
				return
			}
			require.NotNil(t, value)
			require.GreaterOrEqual(t, len(value.Options), 2)
			require.Equal(t, *tt.want, value.Options[0].Value)
			require.True(t, value.Options[0].Recommended)
			require.False(t, value.Options[1].Recommended)
		})
	}
}
