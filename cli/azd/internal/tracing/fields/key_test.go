// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package fields

import (
	"testing"

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
)

func TestErrorKey(t *testing.T) {
	tests := []struct {
		name string
		key  attribute.Key
		want attribute.Key
	}{
		{"PrependsPrefixToNonErrorKey", "service.name", "error.service.name"},
		{"SkipsPrefixForErrorKey", "error.code", "error.code"},
		{"SkipsPrefixForNestedErrorKey", "error.category", "error.category"},
		{"PrependsPrefixToEmptyKey", "", "error."},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, ErrorKey(tt.key))
		})
	}
}

func TestExtensionUsageAttribute(t *testing.T) {
	tests := []struct {
		name string
		key  string
		want attribute.Key
	}{
		{"PrependsPrefix", "deploy.mode", "ext.deploy.mode"},
		{"PrependsPrefixToHostFieldName", "extension.id", "ext.extension.id"},
		{"PrependsPrefixToAlreadyPrefixedKey", "ext.mode", "ext.ext.mode"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			key := ExtensionUsageAttribute(tt.key)

			require.Equal(t, tt.want, key.Key)
			require.Equal(t, SystemMetadata, key.Classification)
			require.Equal(t, FeatureInsight, key.Purpose)
		})
	}
}
