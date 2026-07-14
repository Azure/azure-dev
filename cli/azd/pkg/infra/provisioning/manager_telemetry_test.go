// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package provisioning

import (
	"errors"
	"testing"

	"github.com/azure/azure-dev/cli/azd/internal/tracing"
	"github.com/azure/azure-dev/cli/azd/internal/tracing/fields"
	"github.com/stretchr/testify/require"
)

func TestRecordInfraProviderUsage(t *testing.T) {
	bicepDefault := func() (ProviderKind, error) { return Bicep, nil }
	failingResolver := func() (ProviderKind, error) {
		return NotSpecified, errors.New("no default provider")
	}
	unspecifiedResolver := func() (ProviderKind, error) {
		return NotSpecified, nil
	}

	tests := []struct {
		name            string
		layers          []Options
		defaultProvider DefaultProviderResolver
		expected        string // "" means no infra.provider attribute is recorded
	}{
		{
			name:            "single explicit bicep",
			layers:          []Options{{Provider: Bicep}},
			defaultProvider: bicepDefault,
			expected:        "bicep",
		},
		{
			name:            "single explicit terraform",
			layers:          []Options{{Provider: Terraform}},
			defaultProvider: bicepDefault,
			expected:        "terraform",
		},
		{
			name:            "unspecified resolves through default",
			layers:          []Options{{Provider: NotSpecified}},
			defaultProvider: bicepDefault,
			expected:        "bicep",
		},
		{
			name: "uniform provider across layers",
			layers: []Options{
				{Provider: Bicep},
				{Provider: Bicep},
			},
			defaultProvider: bicepDefault,
			expected:        "bicep",
		},
		{
			name: "different providers across layers records mixed",
			layers: []Options{
				{Provider: Bicep},
				{Provider: Terraform},
			},
			defaultProvider: bicepDefault,
			expected:        InfraProviderMixed,
		},
		{
			name:            "single explicit arm",
			layers:          []Options{{Provider: Arm}},
			defaultProvider: bicepDefault,
			expected:        "arm",
		},
		{
			name:            "custom provider is bucketed",
			layers:          []Options{{Provider: ProviderKind("my-extension-provider")}},
			defaultProvider: bicepDefault,
			expected:        InfraProviderCustom,
		},
		{
			name: "built-in plus custom records mixed",
			layers: []Options{
				{Provider: Bicep},
				{Provider: ProviderKind("my-extension-provider")},
			},
			defaultProvider: bicepDefault,
			expected:        InfraProviderMixed,
		},
		{
			name: "two distinct custom providers record mixed",
			layers: []Options{
				{Provider: ProviderKind("vendor.one")},
				{Provider: ProviderKind("vendor.two")},
			},
			defaultProvider: bicepDefault,
			expected:        InfraProviderMixed,
		},
		{
			name:            "no layers records nothing",
			layers:          nil,
			defaultProvider: bicepDefault,
			expected:        "",
		},
		{
			name:            "unspecified with nil resolver records nothing",
			layers:          []Options{{Provider: NotSpecified}},
			defaultProvider: nil,
			expected:        "",
		},
		{
			name:            "unspecified with failing resolver records nothing",
			layers:          []Options{{Provider: NotSpecified}},
			defaultProvider: failingResolver,
			expected:        "",
		},
		{
			name:            "unspecified resolving to NotSpecified records nothing",
			layers:          []Options{{Provider: NotSpecified}},
			defaultProvider: unspecifiedResolver,
			expected:        "",
		},
	}

	// Usage attributes are process-global, so these subtests must not run in parallel.
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tracing.ResetUsageAttributesForTest()
			t.Cleanup(tracing.ResetUsageAttributesForTest)

			(&Manager{defaultProvider: tt.defaultProvider}).RecordInfraProviderUsage(tt.layers)

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
