// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package telemetry

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// Declaration rules intentionally stay small and use the metadata categories
// already established by core azd fields.
func TestExtensionTelemetryDeclarationRules(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name            string
		declaration     string
		expectedMessage string
	}{
		{
			name: "valid declaration",
			declaration: `var TestField = fields.AttributeKey{
	Key: attribute.Key("ext.test"),
	Classification: fields.SystemMetadata,
	Purpose: fields.FeatureInsight,
	Endpoint: "N/A",
}`,
		},
		{
			name: "public personal data classification is supported",
			declaration: `var TestField = fields.AttributeKey{
	Key: attribute.Key("ext.test"),
	Classification: fields.PublicPersonalData,
	Purpose: fields.FeatureInsight,
	Endpoint: "ReviewedEndpoint",
}`,
		},
		{
			name: "public personal data requires endpoint metadata",
			declaration: `var TestField = fields.AttributeKey{
	Key: attribute.Key("ext.test"),
	Classification: fields.PublicPersonalData,
	Purpose: fields.FeatureInsight,
	Endpoint: "N/A",
}`,
			expectedMessage: "non-SystemMetadata classifications must use an endpoint other than N/A",
		},
		{
			name: "callstack or exception classification is supported",
			declaration: `var TestField = fields.AttributeKey{
	Key: attribute.Key("ext.test"),
	Classification: fields.CallstackOrException,
	Purpose: fields.PerformanceAndHealth,
	Endpoint: "ReviewedEndpoint",
}`,
		},
		{
			name: "customer content classification is supported",
			declaration: `var TestField = fields.AttributeKey{
	Key: attribute.Key("ext.test"),
	Classification: fields.CustomerContent,
	Purpose: fields.FeatureInsight,
	Endpoint: "ReviewedEndpoint",
}`,
		},
		{
			name: "classification alias resolves to the underlying classification",
			declaration: `const SystemMetadata = fields.CustomerContent
var TestField = fields.AttributeKey{
	Key: attribute.Key("ext.test"),
	Classification: SystemMetadata,
	Purpose: fields.FeatureInsight,
	Endpoint: "N/A",
}`,
			expectedMessage: "non-SystemMetadata classifications must use an endpoint other than N/A",
		},
		{
			name: "classification is required",
			declaration: `var TestField = fields.AttributeKey{
	Key: attribute.Key("ext.test"),
	Purpose: fields.FeatureInsight,
	Endpoint: "N/A",
}`,
			expectedMessage: "Classification must use a supported fields.Classification constant",
		},
		{
			name: "purpose is required",
			declaration: `var TestField = fields.AttributeKey{
	Key: attribute.Key("ext.test"),
	Classification: fields.SystemMetadata,
	Endpoint: "N/A",
}`,
			expectedMessage: "Purpose must use a supported fields.Purpose constant",
		},
		{
			name: "endpoint is required",
			declaration: `var TestField = fields.AttributeKey{
	Key: attribute.Key("ext.test"),
	Classification: fields.SystemMetadata,
	Purpose: fields.FeatureInsight,
}`,
			expectedMessage: "Endpoint must be set",
		},
		{
			name: "key uses final extension namespace",
			declaration: `var TestField = fields.AttributeKey{
	Key: attribute.Key("test"),
	Classification: fields.SystemMetadata,
	Purpose: fields.FeatureInsight,
	Endpoint: "N/A",
}`,
			expectedMessage: "must use the final ext.* property name",
		},
		{
			name: "duplicate final key is rejected",
			declaration: `var FirstField = fields.AttributeKey{
	Key: attribute.Key("ext.test"),
	Classification: fields.SystemMetadata,
	Purpose: fields.FeatureInsight,
	Endpoint: "N/A",
}
var SecondField = fields.AttributeKey{
	Key: attribute.Key("ext.test"),
	Classification: fields.SystemMetadata,
	Purpose: fields.FeatureInsight,
	Endpoint: "N/A",
}`,
			expectedMessage: "duplicates extension telemetry key",
		},
		{
			name: "parenthesized measurement",
			declaration: `var TestField = fields.AttributeKey{
	Key: attribute.Key("ext.test"),
	Classification: fields.SystemMetadata,
	Purpose: fields.FeatureInsight,
	Endpoint: "N/A",
	IsMeasurement: (true),
}`,
			expectedMessage: "cannot be declared as measurements",
		},
		{
			name: "measurement constant",
			declaration: `const measurement = true
var TestField = fields.AttributeKey{
	Key: attribute.Key("ext.test"),
	Classification: fields.SystemMetadata,
	Purpose: fields.FeatureInsight,
	Endpoint: "N/A",
	IsMeasurement: measurement,
}`,
			expectedMessage: "cannot be declared as measurements",
		},
		{
			name: "system metadata endpoint must be exact",
			declaration: `var TestField = fields.AttributeKey{
	Key: attribute.Key("ext.test"),
	Classification: fields.SystemMetadata,
	Purpose: fields.FeatureInsight,
	Endpoint: "N/A ",
}`,
			expectedMessage: "SystemMetadata must use endpoint N/A",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			source := `package telemetry
import (
	"go.opentelemetry.io/otel/attribute"
	"github.com/azure/azure-dev/cli/azd/internal/tracing/fields"
)
` + test.declaration
			path := filepath.Join(t.TempDir(), "fields.go")
			require.NoError(t, os.WriteFile(path, []byte(source), 0o600))

			_, diagnostics := loadFieldDeclarations(path)
			if test.expectedMessage == "" {
				require.Empty(t, diagnostics)
			} else {
				require.NotEmpty(t, diagnostics)
				require.Contains(t, strings.Join(diagnostics, "\n"), test.expectedMessage)
			}
		})
	}
}
