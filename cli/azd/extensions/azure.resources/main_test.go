// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package main

import (
	"bytes"
	"encoding/json"
	"os"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func TestRootExposesProviderListener(t *testing.T) {
	root := newRootCommand()
	listen, _, err := root.Find([]string{"listen"})
	require.NoError(t, err)
	require.Equal(t, "listen", listen.Name())
	require.True(t, listen.Hidden)
	require.NotNil(t, listen.RunE)
	require.NoError(t, azdext.ValidateNoReservedFlagConflicts(root))
	for _, name := range []string{"no-prompt", "environment", "cwd", "debug"} {
		require.NotNil(t, root.PersistentFlags().Lookup(name))
	}
}

func TestRootHelpNeedsNoHostConnection(t *testing.T) {
	root := newRootCommand()
	var output bytes.Buffer
	root.SetOut(&output)
	root.SetErr(&output)
	root.SetArgs([]string{"--help"})
	require.NoError(t, root.ExecuteContext(t.Context()))
	require.Contains(t, output.String(), "resources")
}

func TestManifestMatchesExtensionSchema(t *testing.T) {
	schemaBytes, err := os.ReadFile("../extension.schema.json")
	require.NoError(t, err)
	var schema any
	require.NoError(t, json.Unmarshal(schemaBytes, &schema))
	compiler := jsonschema.NewCompiler()
	require.NoError(t, compiler.AddResource("https://example.test/extension.schema.json", schema))
	compiled, err := compiler.Compile("https://example.test/extension.schema.json")
	require.NoError(t, err)
	manifestBytes, err := os.ReadFile("extension.yaml")
	require.NoError(t, err)
	var manifest any
	require.NoError(t, yaml.Unmarshal(manifestBytes, &manifest))
	require.NoError(t, compiled.Validate(manifest))
	var metadata struct {
		Providers []struct {
			Name string `yaml:"name"`
			Type string `yaml:"type"`
		} `yaml:"providers"`
	}
	require.NoError(t, yaml.Unmarshal(manifestBytes, &metadata))
	require.Len(t, metadata.Providers, 1)
	require.Equal(t, "resources", metadata.Providers[0].Name)
	require.Equal(t, "provisioning-provider", metadata.Providers[0].Type)
}
