// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package provisioning_test

import (
	"errors"
	"testing"

	"github.com/azure/azure-dev/cli/azd/internal/tracing"
	"github.com/azure/azure-dev/cli/azd/internal/tracing/fields"
	"github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning"
	"github.com/stretchr/testify/require"
)

func TestRecordInfraProviderUsage(t *testing.T) {
	bicepDefault := func() (provisioning.ProviderKind, error) { return provisioning.Bicep, nil }
	failingResolver := func() (provisioning.ProviderKind, error) {
		return provisioning.NotSpecified, errors.New("no default provider")
	}

	tests := []struct {
		name            string
		layers          []provisioning.Options
		defaultProvider provisioning.DefaultProviderResolver
		expected        string // "" means no infra.provider attribute is recorded
	}{
		{
			name:            "single explicit bicep",
			layers:          []provisioning.Options{{Provider: provisioning.Bicep}},
			defaultProvider: bicepDefault,
			expected:        "bicep",
		},
		{
			name:            "single explicit terraform",
			layers:          []provisioning.Options{{Provider: provisioning.Terraform}},
			defaultProvider: bicepDefault,
			expected:        "terraform",
		},
		{
			name:            "unspecified resolves through default",
			layers:          []provisioning.Options{{Provider: provisioning.NotSpecified}},
			defaultProvider: bicepDefault,
			expected:        "bicep",
		},
		{
			name: "uniform provider across layers",
			layers: []provisioning.Options{
				{Provider: provisioning.Bicep},
				{Provider: provisioning.Bicep},
			},
			defaultProvider: bicepDefault,
			expected:        "bicep",
		},
		{
			name: "different providers across layers records mixed",
			layers: []provisioning.Options{
				{Provider: provisioning.Bicep},
				{Provider: provisioning.Terraform},
			},
			defaultProvider: bicepDefault,
			expected:        provisioning.InfraProviderMixed,
		},
		{
			name:            "no layers records nothing",
			layers:          nil,
			defaultProvider: bicepDefault,
			expected:        "",
		},
		{
			name:            "unspecified with nil resolver records nothing",
			layers:          []provisioning.Options{{Provider: provisioning.NotSpecified}},
			defaultProvider: nil,
			expected:        "",
		},
		{
			name:            "unspecified with failing resolver records nothing",
			layers:          []provisioning.Options{{Provider: provisioning.NotSpecified}},
			defaultProvider: failingResolver,
			expected:        "",
		},
	}

	// Usage attributes are process-global, so these subtests must not run in parallel.
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tracing.ResetUsageAttributesForTest()
			t.Cleanup(tracing.ResetUsageAttributesForTest)

			provisioning.RecordInfraProviderUsage(tt.layers, tt.defaultProvider)

			var got string
			var found bool
			for _, attr := range tracing.GetUsageAttributes() {
				if attr.Key == fields.InfraProviderKey.Key {
					got = attr.Value.AsString()
					found = true
				}
			}

			if tt.expected == "" {
				require.False(t, found, "expected no infra.provider attribute, got %q", got)
				return
			}

			require.True(t, found, "expected infra.provider attribute to be recorded")
			require.Equal(t, tt.expected, got)
		})
	}
}
