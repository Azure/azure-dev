// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package extensions

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestClassifySource(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		source   *SourceConfig
		expected SourceCategory
	}{
		{name: "missing", expected: SourceCategoryUnknown},
		{
			name:     "local file",
			source:   &SourceConfig{Name: "custom", Type: SourceKindFile, Location: "/private/registry.json"},
			expected: SourceCategoryLocal,
		},
		{
			name:     "bundle",
			source:   &SourceConfig{Name: "temporary", Type: SourceKindBundle, Location: "/private/bundle"},
			expected: SourceCategoryBundle,
		},
		{
			name:     "azd aka ms",
			source:   &SourceConfig{Name: "team-registry", Type: SourceKindUrl, Location: extensionRegistryUrl},
			expected: SourceCategoryAzd,
		},
		{
			name: "azd resolved",
			source: &SourceConfig{
				Name: "custom",
				Type: SourceKindUrl,
				Location: "https://raw.githubusercontent.com/Azure/azure-dev/refs/heads/main/" +
					"cli/azd/extensions/registry.json",
			},
			expected: SourceCategoryAzd,
		},
		{
			name: "dev aka ms",
			source: &SourceConfig{
				Name:     "not-dev",
				Type:     SourceKindUrl,
				Location: "https://aka.ms/azd/extensions/registry/dev/",
			},
			expected: SourceCategoryDev,
		},
		{
			name: "dev resolved",
			source: &SourceConfig{
				Name: "custom",
				Type: SourceKindUrl,
				Location: "HTTPS://RAW.GITHUBUSERCONTENT.COM/Azure/Azure-Dev/main/" +
					"cli/azd/extensions/registry.dev.json",
			},
			expected: SourceCategoryDev,
		},
		{
			name: "nightly aka ms",
			source: &SourceConfig{
				Name:     "custom",
				Type:     SourceKindUrl,
				Location: "https://aka.ms/azd/extensions/registry/nightly",
			},
			expected: SourceCategoryNightly,
		},
		{
			name: "nightly resolved",
			source: &SourceConfig{
				Name: "dev",
				Type: SourceKindUrl,
				Location: "https://raw.githubusercontent.com/Azure/azure-dev/nightly/" +
					"cli/azd/extensions/registry.nightly.json",
			},
			expected: SourceCategoryNightly,
		},
		{
			name: "custom url",
			source: &SourceConfig{
				Name:     "azd",
				Type:     SourceKindUrl,
				Location: "https://customer.example/registry.json",
			},
			expected: SourceCategoryOther,
		},
		{
			name: "known path with query is other",
			source: &SourceConfig{
				Name:     "custom",
				Type:     SourceKindUrl,
				Location: extensionRegistryUrl + "?redirect=custom",
			},
			expected: SourceCategoryOther,
		},
		{
			name:     "malformed url",
			source:   &SourceConfig{Name: "custom", Type: SourceKindUrl, Location: "https://example.com/%zz"},
			expected: SourceCategoryOther,
		},
		{
			name:     "custom kind",
			source:   &SourceConfig{Name: "custom", Type: SourceKind("plugin"), Location: "opaque"},
			expected: SourceCategoryOther,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.expected, ClassifySource(tt.source))
		})
	}
}

func TestSourceCategoryOrUnknown(t *testing.T) {
	t.Parallel()

	var extension *Extension
	var metadata *ExtensionMetadata
	require.Equal(t, SourceCategoryUnknown, extension.SourceCategoryOrUnknown())
	require.Equal(t, SourceCategoryUnknown, metadata.SourceCategoryOrUnknown())
	require.Equal(t, SourceCategoryUnknown, (&Extension{}).SourceCategoryOrUnknown())
	require.Equal(t, SourceCategoryUnknown, (&Extension{SourceCategory: "invalid"}).SourceCategoryOrUnknown())
	require.Equal(t, SourceCategoryDev, (&Extension{SourceCategory: SourceCategoryDev}).SourceCategoryOrUnknown())
	require.Equal(t, SourceCategoryUnknown, (&ExtensionMetadata{}).SourceCategoryOrUnknown())
	require.Equal(
		t,
		SourceCategoryLocal,
		(&ExtensionMetadata{SourceCategory: SourceCategoryLocal}).SourceCategoryOrUnknown(),
	)
}
