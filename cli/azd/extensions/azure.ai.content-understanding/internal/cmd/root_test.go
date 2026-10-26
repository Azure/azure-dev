// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/extensions"
	schemaReflect "github.com/invopop/jsonschema"
	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.yaml.in/yaml/v3"
	"google.golang.org/grpc/metadata"
)

func executeRoot(t *testing.T, args ...string) (string, string, error) {
	t.Helper()
	root := NewRootCommand()
	var stdout, stderr bytes.Buffer
	root.SetOut(&stdout)
	root.SetErr(&stderr)
	root.SetArgs(args)
	if err := ValidateInvocation(root, args); err != nil {
		return "", "", err
	}
	err := root.ExecuteContext(t.Context())
	return stdout.String(), stderr.String(), err
}

func TestRootWithoutHostOrProject(t *testing.T) {
	for _, name := range []string{
		"AZD_SERVER", "AZD_ACCESS_TOKEN", "AZD_CWD", "AZD_ENVIRONMENT", "AZD_DEBUG", "AZD_NO_PROMPT",
	} {
		t.Setenv(name, "")
	}
	for _, tt := range []struct {
		name string
		args []string
		want string
	}{
		{"help", []string{"--help"}, "Azure Content Understanding"},
		{"version", []string{"version"}, extensionID + " " + Version + "\n"},
		{"version flag", []string{"--version"}, Version},
		{"json version", []string{"version", "--output", "json"}, `"name":"` + extensionID + `"`},
		{"metadata", []string{"metadata"}, `"id": "` + extensionID + `"`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			stdout, stderr, err := executeRoot(t, tt.args...)
			require.NoError(t, err)
			assert.Contains(t, stdout, tt.want)
			assert.Empty(t, stderr)
		})
	}
}

func TestRootHelpSnapshot(t *testing.T) {
	actual, stderr, err := executeRoot(t, "--help")
	require.NoError(t, err)
	assert.Empty(t, stderr)
	if os.Getenv("UPDATE_SNAPSHOTS") == "true" {
		require.NoError(t, os.WriteFile("testdata/help.txt", []byte(actual), 0o600))
	}
	expected, err := os.ReadFile("testdata/help.txt")
	require.NoError(t, err)
	assert.Equal(t, string(expected), actual)
}

func TestUsageErrors(t *testing.T) {
	for _, tt := range []struct {
		name string
		args []string
	}{
		{"unknown command", []string{"missing-command"}},
		{"unknown flag", []string{"version", "--missing"}},
		{"invalid bool", []string{"version", "--debug=invalid"}},
		{"invalid output", []string{"version", "--output", "table"}},
		{"version arguments", []string{"version", "extra"}},
		{"metadata arguments", []string{"metadata", "extra"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			stdout, stderr, err := executeRoot(t, tt.args...)
			require.ErrorContains(t, err, "The command arguments or options are invalid.")
			local, ok := errors.AsType[*azdext.LocalError](err)
			require.True(t, ok)
			assert.Equal(t, azdext.LocalErrorCategoryValidation, local.Category)
			assert.Equal(t, "invalid_command_arguments", local.Code)
			assert.Empty(t, stdout)
			assert.Empty(t, stderr)
		})
	}
}

func TestReservedFlags(t *testing.T) {
	root := NewRootCommand()
	require.NoError(t, azdext.ValidateNoReservedFlagConflicts(root))
	for _, name := range []string{"debug", "no-prompt", "cwd", "environment", "output"} {
		flag := root.PersistentFlags().Lookup(name)
		require.NotNil(t, flag)
		for _, cmd := range root.Commands() {
			cmd.InheritedFlags()
			assert.Same(t, flag, cmd.Flags().Lookup(name))
			assert.Nil(t, cmd.LocalNonPersistentFlags().Lookup(name))
		}
	}
}

func TestSDKContextAndGlobalValues(t *testing.T) {
	t.Setenv("AZD_DEBUG", "true")
	t.Setenv("AZD_NO_PROMPT", "true")
	t.Setenv("AZD_ENVIRONMENT", "selected-environment")
	t.Setenv("AZD_ACCESS_TOKEN", "private-host-token")
	for _, tt := range []struct {
		name  string
		flags []string
		debug bool
		env   string
	}{
		{"environment defaults", nil, true, "selected-environment"},
		{"explicit overrides", []string{"--debug=false", "--environment", "explicit-environment"},
			false, "explicit-environment"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			root := NewRootCommand()
			root.AddCommand(&cobra.Command{
				Use: "probe",
				RunE: func(cmd *cobra.Command, _ []string) error {
					md, _ := metadata.FromOutgoingContext(cmd.Context())
					assert.Equal(t, []string{"private-host-token"}, md.Get("authorization"))
					assert.Equal(t, t.Context().Done(), cmd.Context().Done())
					return nil
				},
			})
			root.SetArgs(append([]string{"probe"}, tt.flags...))
			require.NoError(t, root.ExecuteContext(t.Context()))
			debug, err := root.PersistentFlags().GetBool("debug")
			require.NoError(t, err)
			assert.Equal(t, tt.debug, debug)
			noPrompt, err := root.PersistentFlags().GetBool("no-prompt")
			require.NoError(t, err)
			assert.True(t, noPrompt)
			environment, err := root.PersistentFlags().GetString("environment")
			require.NoError(t, err)
			assert.Equal(t, tt.env, environment)
		})
	}
}

func TestMetadataUsesRuntimeTree(t *testing.T) {
	root := NewRootCommand()
	root.AddCommand(&cobra.Command{Use: "test-child", Short: "Added at runtime"})
	var stdout, stderr bytes.Buffer
	root.SetOut(&stdout)
	root.SetErr(&stderr)
	root.SetArgs([]string{"metadata"})
	require.NoError(t, root.ExecuteContext(t.Context()))

	var actual extensions.ExtensionCommandMetadata
	require.NoError(t, json.Unmarshal(stdout.Bytes(), &actual))
	var document map[string]any
	require.NoError(t, json.Unmarshal(stdout.Bytes(), &document))
	// Embedded configuration schemas are opaque JSON objects in the command metadata contract.
	reflector := schemaReflect.Reflector{IgnoredTypes: []any{schemaReflect.Schema{}}}
	schema := reflector.Reflect(&actual)
	require.NoError(t, extensions.ValidateAgainstSchema(schema, document))
	assert.Equal(t, "1.0", actual.SchemaVersion)
	assert.Equal(t, extensionID, actual.ID)
	assert.Empty(t, stderr.String())
	found := false
	for _, cmd := range actual.Commands {
		if strings.Join(cmd.Name, " ") == "test-child" {
			found = true
			assert.Equal(t, "Added at runtime", cmd.Short)
		}
	}
	assert.True(t, found, "metadata must include commands added to the runtime tree")
	document["commands"] = []any{map[string]any{"name": []any{42}, "short": "invalid command"}}
	assert.Error(t, extensions.ValidateAgainstSchema(schema, document))
}

func TestVersionJSONShape(t *testing.T) {
	stdout, stderr, err := executeRoot(t, "version", "--output", "json")
	require.NoError(t, err)
	assert.Empty(t, stderr)
	var document map[string]string
	require.NoError(t, json.Unmarshal([]byte(stdout), &document))
	assert.Equal(t, map[string]string{"name": extensionID, "version": Version}, document)
}

func TestManifestSchemaAndIdentity(t *testing.T) {
	content, err := os.ReadFile("../../extension.yaml")
	require.NoError(t, err)
	var manifest map[string]any
	require.NoError(t, yaml.Unmarshal(content, &manifest))
	schemaPath, err := filepath.Abs("../../../extension.schema.json")
	require.NoError(t, err)
	schema, err := jsonschema.NewCompiler().Compile(schemaPath)
	require.NoError(t, err)
	require.NoError(t, schema.Validate(manifest))
	assert.Equal(t, extensionID, manifest["id"])
	assert.Equal(t, "ai.cu", manifest["namespace"])
	assert.Equal(t, "go", manifest["language"])
	assert.ElementsMatch(t, []any{"custom-commands", "metadata"}, manifest["capabilities"])
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) {
	return 0, errors.New("output unavailable")
}

func TestOutputFailures(t *testing.T) {
	for _, args := range [][]string{{"metadata"}, {"version"}, {"version", "--output", "json"}} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			root := NewRootCommand()
			root.SetOut(failingWriter{})
			root.SetArgs(args)
			err := root.ExecuteContext(context.Background())
			require.ErrorContains(t, err, "could not complete the operation")
			assert.NotContains(t, err.Error(), "output unavailable")
		})
	}
}
