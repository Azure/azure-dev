// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package extensions

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/Masterminds/semver/v3"
	jsonschemav6 "github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/stretchr/testify/require"
)

func TestRegistrySchemaStrictSemanticVersions(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "extensions", "registry.schema.json"))
	require.NoError(t, err)
	var document any
	require.NoError(t, json.Unmarshal(data, &document))
	const resource = "mem://registry.schema.json"
	compiler := jsonschemav6.NewCompiler()
	require.NoError(t, compiler.AddResource(resource, document))

	tests := []struct {
		version string
		valid   bool
	}{
		{"0.0.0", true},
		{"1.0.47-beta", true},
		{"1.0.0-beta.1", true},
		{"1.0.0-0", true},
		{"1.0.0-alpha.0", true},
		{"1.0.0-01a", true},
		{"1.0.0-00-01", true},
		{"1.0.0+01.001", true},
		{"1.0.0-beta.1+build.001", true},
		{"01.0.0", false},
		{"1.01.0", false},
		{"1.0.01", false},
		{"1.0.0-01", false},
		{"1.0.0-alpha.01", false},
		{"1.0.0-00", false},
		{"1.0.0-", false},
		{"1.0.0+", false},
		{"1.0.0-alpha..1", false},
		{"v1.0.0", false},
		{"1.0", false},
		{"nightly", false},
	}
	for _, property := range []string{
		"Version/properties/version",
		"VersionMigration/properties/from",
		"VersionMigration/properties/to",
	} {
		t.Run(property, func(t *testing.T) {
			schema, err := compiler.Compile(resource + "#/definitions/" + property)
			require.NoError(t, err)
			for _, tt := range tests {
				t.Run(tt.version, func(t *testing.T) {
					_, runtimeErr := semver.StrictNewVersion(tt.version)
					require.Equal(t, tt.valid, runtimeErr == nil, "strict runtime control")
					err := schema.Validate(tt.version)
					require.Equal(t, tt.valid, err == nil, "schema/runtime disagreement: %v", err)
				})
			}
		})
	}
}
