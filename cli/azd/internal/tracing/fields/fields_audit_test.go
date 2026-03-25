// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package fields

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestNewFieldConstantsDefined verifies that all new telemetry field constants added
// as part of the metrics audit are properly defined with correct metadata.
func TestNewFieldConstantsDefined(t *testing.T) {
	tests := []struct {
		name           string
		key            AttributeKey
		expectedKey    string
		classification Classification
		purpose        Purpose
		isMeasurement  bool
	}{
		// Auth fields
		{
			name:           "AuthMethodKey",
			key:            AuthMethodKey,
			expectedKey:    "auth.method",
			classification: SystemMetadata,
			purpose:        FeatureInsight,
		},
		// Env fields
		{
			name:           "EnvCountKey",
			key:            EnvCountKey,
			expectedKey:    "env.count",
			classification: SystemMetadata,
			purpose:        FeatureInsight,
			isMeasurement:  true,
		},
		// Hooks fields
		{
			name:           "HooksNameKey",
			key:            HooksNameKey,
			expectedKey:    "hooks.name",
			classification: SystemMetadata,
			purpose:        FeatureInsight,
		},
		{
			name:           "HooksTypeKey",
			key:            HooksTypeKey,
			expectedKey:    "hooks.type",
			classification: SystemMetadata,
			purpose:        FeatureInsight,
		},
		// Pipeline fields
		{
			name:           "PipelineProviderKey",
			key:            PipelineProviderKey,
			expectedKey:    "pipeline.provider",
			classification: SystemMetadata,
			purpose:        FeatureInsight,
		},
		{
			name:           "PipelineAuthKey",
			key:            PipelineAuthKey,
			expectedKey:    "pipeline.auth",
			classification: SystemMetadata,
			purpose:        FeatureInsight,
		},
		// Infra fields
		{
			name:           "InfraProviderKey",
			key:            InfraProviderKey,
			expectedKey:    "infra.provider",
			classification: SystemMetadata,
			purpose:        FeatureInsight,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.expectedKey, string(tt.key.Key), "Key value mismatch")
			require.Equal(t, tt.classification, tt.key.Classification, "Classification mismatch")
			require.Equal(t, tt.purpose, tt.key.Purpose, "Purpose mismatch")
			require.Equal(t, tt.isMeasurement, tt.key.IsMeasurement, "IsMeasurement mismatch")
		})
	}
}
