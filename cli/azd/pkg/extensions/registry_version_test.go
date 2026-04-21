// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package extensions

import (
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCheckRegistrySchemaVersion(t *testing.T) {
	tests := []struct {
		name        string
		version     string
		expectErr   bool
		expectType  bool // true if error should be ErrUnsupportedRegistrySchema
		errContains string
	}{
		{
			name:      "empty version assumes 1.0",
			version:   "",
			expectErr: false,
		},
		{
			name:      "current version 1.0",
			version:   "1.0",
			expectErr: false,
		},
		{
			name:      "forward-compatible minor 1.1",
			version:   "1.1",
			expectErr: false,
		},
		{
			name:      "forward-compatible minor 1.99",
			version:   "1.99",
			expectErr: false,
		},
		{
			name:       "future incompatible major 2.0",
			version:    "2.0",
			expectErr:  true,
			expectType: true,
		},
		{
			name:       "future incompatible major 3.5",
			version:    "3.5",
			expectErr:  true,
			expectType: true,
		},
		{
			name:        "malformed: letters",
			version:     "abc",
			expectErr:   true,
			errContains: "expected major.minor format",
		},
		{
			name:        "malformed: single number",
			version:     "1",
			expectErr:   true,
			errContains: "expected major.minor format",
		},
		{
			name:        "malformed: three segments",
			version:     "1.0.0",
			expectErr:   true,
			errContains: "expected major.minor format",
		},
		{
			name:        "malformed: v-prefix",
			version:     "v1.0",
			expectErr:   true,
			errContains: "cannot parse major component",
		},
		{
			name:        "malformed: non-numeric minor",
			version:     "1.x",
			expectErr:   true,
			errContains: "cannot parse minor component",
		},
		{
			name:      "major version 0",
			version:   "0.1",
			expectErr: false,
		},
		{
			name:      "version 1.0 exact match",
			version:   "1.0",
			expectErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := CheckRegistrySchemaVersion(tt.version)
			if !tt.expectErr {
				require.NoError(t, err)
				return
			}

			require.Error(t, err)

			if tt.expectType {
				schemaErr, ok := errors.AsType[*ErrUnsupportedRegistrySchema](err)
				require.True(t, ok, "expected ErrUnsupportedRegistrySchema")
				assert.Equal(t, tt.version, schemaErr.SchemaVersion)
				assert.Equal(
					t,
					CurrentRegistrySchemaVersion,
					schemaErr.MaxSupportedVersion,
				)
			}

			if tt.errContains != "" {
				assert.Contains(t, err.Error(), tt.errContains)
			}
		})
	}
}

func TestErrUnsupportedRegistrySchema_ErrorMessage(t *testing.T) {
	err := &ErrUnsupportedRegistrySchema{
		SchemaVersion:       "2.0",
		MaxSupportedVersion: "1.0",
	}

	msg := err.Error()
	assert.Contains(t, msg, "2.0")
	assert.Contains(t, msg, "1.0")
	assert.Contains(t, msg, "not supported")
}

func TestNewJsonSource_IncompatibleSchemaVersion(t *testing.T) {
	registry := map[string]any{
		"schemaVersion": "2.0",
		"extensions":    []any{},
	}
	data, err := json.Marshal(registry)
	require.NoError(t, err)

	_, err = newJsonSource("test", string(data))
	require.Error(t, err)

	schemaErr, ok := errors.AsType[*ErrUnsupportedRegistrySchema](err)
	require.True(t, ok, "expected ErrUnsupportedRegistrySchema")
	assert.Equal(t, "2.0", schemaErr.SchemaVersion)
}

func TestNewJsonSource_CompatibleSchemaVersion(t *testing.T) {
	registry := map[string]any{
		"schemaVersion": "1.0",
		"extensions":    []any{},
	}
	data, err := json.Marshal(registry)
	require.NoError(t, err)

	source, err := newJsonSource("test", string(data))
	require.NoError(t, err)
	require.NotNil(t, source)
}

func TestNewJsonSource_MissingSchemaVersion(t *testing.T) {
	registry := map[string]any{
		"extensions": []any{},
	}
	data, err := json.Marshal(registry)
	require.NoError(t, err)

	source, err := newJsonSource("test", string(data))
	require.NoError(t, err)
	require.NotNil(t, source)
}

func TestNewJsonSource_ForwardCompatibleMinor(t *testing.T) {
	registry := map[string]any{
		"schemaVersion": "1.5",
		"extensions":    []any{},
	}
	data, err := json.Marshal(registry)
	require.NoError(t, err)

	source, err := newJsonSource("test", string(data))
	require.NoError(t, err)
	require.NotNil(t, source)
}

func TestRegistrySchemaVersionSerialization(t *testing.T) {
	t.Run("schemaVersion is parsed from JSON", func(t *testing.T) {
		jsonData := `{
			"schemaVersion": "1.0",
			"extensions": []
		}`

		var registry Registry
		err := json.Unmarshal([]byte(jsonData), &registry)
		require.NoError(t, err)
		assert.Equal(t, "1.0", registry.SchemaVersion)
	})

	t.Run("schemaVersion is omitted when empty", func(t *testing.T) {
		registry := Registry{
			Extensions: []*ExtensionMetadata{},
		}

		data, err := json.Marshal(registry)
		require.NoError(t, err)
		assert.NotContains(t, string(data), `"schemaVersion"`)
	})

	t.Run("schemaVersion is included when set", func(t *testing.T) {
		registry := Registry{
			SchemaVersion: "1.0",
			Extensions:    []*ExtensionMetadata{},
		}

		data, err := json.Marshal(registry)
		require.NoError(t, err)
		assert.Contains(t, string(data), `"schemaVersion":"1.0"`)
	})
}

func TestValidateRegistry_SchemaVersion(t *testing.T) {
	validExt := &ExtensionMetadata{
		Id:          "publisher.test",
		DisplayName: "Test",
		Description: "A test extension",
		Versions: []ExtensionVersion{{
			Version:   "1.0.0",
			Artifacts: validArtifacts(),
		}},
	}

	tests := []struct {
		name           string
		schemaVersion  string
		expectValid    bool
		expectWarnings int
		expectErrors   int
	}{
		{
			name:           "missing schemaVersion produces warning",
			schemaVersion:  "",
			expectValid:    true,
			expectWarnings: 1,
		},
		{
			name:          "valid schemaVersion 1.0",
			schemaVersion: "1.0",
			expectValid:   true,
		},
		{
			name:          "valid schemaVersion 2.5",
			schemaVersion: "2.5",
			expectValid:   true,
		},
		{
			name:          "invalid format produces error",
			schemaVersion: "abc",
			expectValid:   false,
			expectErrors:  1,
		},
		{
			name:          "three segments invalid",
			schemaVersion: "1.0.0",
			expectValid:   false,
			expectErrors:  1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			registry := &Registry{
				SchemaVersion: tt.schemaVersion,
				Extensions:    []*ExtensionMetadata{validExt},
			}

			result := ValidateRegistry(registry, false)
			assert.Equal(t, tt.expectValid, result.Valid)

			warnings := 0
			errs := 0
			for _, issue := range result.Issues {
				switch issue.Severity {
				case ValidationWarning:
					warnings++
				case ValidationError:
					errs++
				}
			}

			assert.Equal(t, tt.expectWarnings, warnings,
				"unexpected warning count")
			assert.Equal(t, tt.expectErrors, errs,
				"unexpected error count")
		})
	}
}

func TestParseSchemaVersion(t *testing.T) {
	tests := []struct {
		name        string
		version     string
		wantMajor   int
		wantMinor   int
		expectErr   bool
		errContains string
	}{
		{
			name:      "standard 1.0",
			version:   "1.0",
			wantMajor: 1,
			wantMinor: 0,
		},
		{
			name:      "higher minor",
			version:   "1.5",
			wantMajor: 1,
			wantMinor: 5,
		},
		{
			name:      "major 2",
			version:   "2.0",
			wantMajor: 2,
			wantMinor: 0,
		},
		{
			name:        "no dot",
			version:     "1",
			expectErr:   true,
			errContains: "expected major.minor",
		},
		{
			name:        "triple dot",
			version:     "1.2.3",
			expectErr:   true,
			errContains: "expected major.minor",
		},
		{
			name:        "alpha major",
			version:     "a.0",
			expectErr:   true,
			errContains: "cannot parse major",
		},
		{
			name:        "alpha minor",
			version:     "1.b",
			expectErr:   true,
			errContains: "cannot parse minor",
		},
		{
			name:        "negative major",
			version:     "-1.0",
			expectErr:   true,
			errContains: "major version cannot be negative",
		},
		{
			name:        "negative minor",
			version:     "1.-5",
			expectErr:   true,
			errContains: "minor version cannot be negative",
		},
		{
			name:        "negative both",
			version:     "-5.-10",
			expectErr:   true,
			errContains: "major version cannot be negative",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			major, minor, err := parseSchemaVersion(tt.version)
			if tt.expectErr {
				require.Error(t, err)
				if tt.errContains != "" {
					assert.Contains(t, err.Error(), tt.errContains)
				}
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.wantMajor, major)
			assert.Equal(t, tt.wantMinor, minor)
		})
	}
}

func TestErrUnsupportedRegistrySchema_Formatting(t *testing.T) {
	err := &ErrUnsupportedRegistrySchema{
		SchemaVersion:       "3.0",
		MaxSupportedVersion: "1.0",
	}

	expected := fmt.Sprintf(
		"registry schema version %s is not supported (max supported: %s)",
		"3.0", "1.0",
	)
	assert.Equal(t, expected, err.Error())
}
