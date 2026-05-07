// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package ai

import (
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/cognitiveservices/armcognitiveservices"
	"github.com/stretchr/testify/require"
)

func TestModelLifecycleDeprecated(t *testing.T) {
	t.Parallel()

	deprecated := armcognitiveservices.ModelLifecycleStatus("Deprecated")
	deprecatedLower := armcognitiveservices.ModelLifecycleStatus("deprecated")
	preview := armcognitiveservices.ModelLifecycleStatus("Preview")

	tests := []struct {
		name   string
		status *armcognitiveservices.ModelLifecycleStatus
		want   bool
	}{
		{"nil status returns false", nil, false},
		{"deprecated returns true", &deprecated, true},
		{"case-insensitive deprecated returns true", &deprecatedLower, true},
		{"non-deprecated returns false", &preview, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, modelLifecycleDeprecated(tt.status))
		})
	}
}

func TestModelLifecycleStatusValue(t *testing.T) {
	t.Parallel()

	status := armcognitiveservices.ModelLifecycleStatus("GenerallyAvailable")

	require.Equal(t, "", modelLifecycleStatusValue(nil))
	require.Equal(t, "GenerallyAvailable", modelLifecycleStatusValue(&status))
}

func TestModelDeprecationReached(t *testing.T) {
	t.Parallel()

	now := time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC)

	past := "2025-01-01T00:00:00Z"
	future := "2026-01-01T00:00:00Z"

	tests := []struct {
		name string
		info *armcognitiveservices.ModelDeprecationInfo
		want bool
	}{
		{"nil info returns false", nil, false},
		{
			name: "nil inference field returns false",
			info: &armcognitiveservices.ModelDeprecationInfo{Inference: nil},
			want: false,
		},
		{
			name: "past inference returns true",
			info: &armcognitiveservices.ModelDeprecationInfo{Inference: &past},
			want: true,
		},
		{
			name: "future inference returns false",
			info: &armcognitiveservices.ModelDeprecationInfo{Inference: &future},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, modelDeprecationReached(tt.info, now))
		})
	}
}

func TestDeprecationReached(t *testing.T) {
	t.Parallel()

	now := time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC)

	tests := []struct {
		name  string
		value string
		want  bool
	}{
		{"empty string returns false", "", false},
		{"whitespace-only returns false", "   ", false},
		{"unparseable returns false", "not-a-date", false},
		{"past returns true", "2025-01-01T00:00:00Z", true},
		{"future returns false", "2030-01-01T00:00:00Z", false},
		{"exactly now returns true (not After)", "2025-06-01T00:00:00Z", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, deprecationReached(tt.value, now))
		})
	}
}

func TestModelVersionDeprecated(t *testing.T) {
	t.Parallel()

	now := time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC)

	deprecated := armcognitiveservices.ModelLifecycleStatus("Deprecated")
	ga := armcognitiveservices.ModelLifecycleStatus("GenerallyAvailable")
	past := "2025-01-01T00:00:00Z"
	future := "2030-01-01T00:00:00Z"

	tests := []struct {
		name  string
		model *armcognitiveservices.AccountModel
		want  bool
	}{
		{"nil model returns false", nil, false},
		{
			name:  "deprecated lifecycle returns true",
			model: &armcognitiveservices.AccountModel{LifecycleStatus: &deprecated},
			want:  true,
		},
		{
			name: "past deprecation returns true",
			model: &armcognitiveservices.AccountModel{
				LifecycleStatus: &ga,
				Deprecation:     &armcognitiveservices.ModelDeprecationInfo{Inference: &past},
			},
			want: true,
		},
		{
			name: "future deprecation returns false",
			model: &armcognitiveservices.AccountModel{
				LifecycleStatus: &ga,
				Deprecation:     &armcognitiveservices.ModelDeprecationInfo{Inference: &future},
			},
			want: false,
		},
		{
			name:  "no lifecycle no deprecation returns false",
			model: &armcognitiveservices.AccountModel{},
			want:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, modelVersionDeprecated(tt.model, now))
		})
	}
}

func TestModelSkuDeprecated(t *testing.T) {
	t.Parallel()

	now := time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC)
	past := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	future := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)

	tests := []struct {
		name string
		sku  *armcognitiveservices.ModelSKU
		want bool
	}{
		{"nil sku returns false", nil, false},
		{"nil deprecation date returns false", &armcognitiveservices.ModelSKU{DeprecationDate: nil}, false},
		{"past deprecation date returns true", &armcognitiveservices.ModelSKU{DeprecationDate: &past}, true},
		{"future deprecation date returns false", &armcognitiveservices.ModelSKU{DeprecationDate: &future}, false},
		{"exactly now returns true (not After)", &armcognitiveservices.ModelSKU{DeprecationDate: &now}, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, modelSkuDeprecated(tt.sku, now))
		})
	}
}
