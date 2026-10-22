// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"testing"

	"azure.ai.connections/internal/foundry"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConnectionAddCommandMultipleProjectsKeepsJSONOutputClean(t *testing.T) {
	root := t.TempDir()
	file := writeConnectionAddDefinition(t, root, "connection.yaml",
		"category: Search\ntarget: https://search.example.test\n")
	client := newConnectionAddProjectClientStub()
	client.services["first"] = map[string]any{"host": aiProjectServiceHost}
	client.services["second"] = map[string]any{"host": aiProjectServiceHost}
	var stdout, stderr bytes.Buffer
	selected := false
	command := newConnectionAddCommandWithDependencies(
		&azdext.ExtensionContext{OutputFormat: "json"},
		connectionAddDependencies{
			newRuntime: func() (*connectionAddRuntime, error) {
				return &connectionAddRuntime{
					project: client,
					selectProject: func(_ context.Context, keys []string) (string, error) {
						assert.Equal(t, []string{"first", "second"}, keys)
						selected = true
						_, err := stderr.WriteString("Select the Foundry Project service\n")
						return "second", err
					},
					close: func() {},
				}, nil
			},
			projectDir: func() (string, error) { return root, nil },
		},
	)
	command.SetOut(&stdout)
	command.SetErr(&stderr)
	command.SetArgs([]string{"search", "--file", file})
	require.NoError(t, command.ExecuteContext(t.Context()))
	require.True(t, selected)
	var result map[string]any
	require.NoError(t, json.Unmarshal(stdout.Bytes(), &result))
	assert.Equal(t, "second", result["project"])
	assert.Equal(t, "added", result["mutation"])
	assert.NotContains(t, stdout.String(), "Select")
	assert.Contains(t, stderr.String(), "Select the Foundry Project service")
}

func TestConnectionAddCommandWritesStableJSONToInjectedWriter(t *testing.T) {
	root := t.TempDir()
	file := writeConnectionAddDefinition(t, root, "connection.yaml", `
category: CognitiveSearch
target: https://search.example.test
authType: ApiKey
credentials:
  key: ${SEARCH_KEY}
`)
	client := newConnectionAddProjectClientStub()
	client.services["foundry"] = map[string]any{"host": aiProjectServiceHost}
	runtimeCreated := 0
	command := newConnectionAddCommandWithDependencies(
		&azdext.ExtensionContext{OutputFormat: "json"},
		connectionAddDependencies{
			newRuntime: func() (*connectionAddRuntime, error) {
				runtimeCreated++
				return &connectionAddRuntime{
					project: client,
					close:   func() {},
				}, nil
			},
			projectDir: func() (string, error) { return root, nil },
			loadDefinition: func(projectRoot, path string) (map[string]any, string, error) {
				return foundry.DecodeContainedDocument(projectRoot, path)
			},
		},
	)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	command.SetOut(&stdout)
	command.SetErr(&stderr)
	command.SetArgs([]string{"search", "--file", file})

	require.NoError(t, command.ExecuteContext(t.Context()))
	assert.Equal(
		t,
		"{\"name\":\"search\",\"host\":\"azure.ai.connection\",\"mutation\":\"added\","+
			"\"ref\":\"./connection.yaml\",\"project\":\"foundry\","+
			"\"environmentVariables\":[\"SEARCH_KEY\"]}\n",
		stdout.String(),
	)
	assert.Empty(t, stderr.String())
	assert.Equal(t, 1, runtimeCreated)
	require.Len(t, client.addRequests, 1)
	assert.Equal(t, []string{"add", "set:env"}, client.mutationCalls)
}

func TestConnectionAddCommandRejectsIneffectiveFlagsBeforeClientCreation(t *testing.T) {
	tests := []struct {
		name      string
		args      []string
		wantError string
	}{
		{
			name:      "environment selector",
			args:      []string{"add", "search", "--file", "connection.yaml", "--environment", "dev"},
			wantError: "--environment is not supported",
		},
		{
			name:      "remote endpoint selector",
			args:      []string{"add", "search", "--file", "connection.yaml", "--project-endpoint", "https://example.test"},
			wantError: "--project-endpoint is not supported",
		},
		{
			name:      "unsupported output format",
			args:      []string{"add", "search", "--file", "connection.yaml", "--output", "yaml"},
			wantError: "invalid value \"yaml\" for --output",
		},
		{
			name:      "empty output format",
			args:      []string{"add", "search", "--file", "connection.yaml", "--output", ""},
			wantError: "invalid value \"\" for --output",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := NewRootCommand()
			root.SetArgs(test.args)
			root.SetOut(io.Discard)
			root.SetErr(io.Discard)
			require.ErrorContains(t, root.ExecuteContext(t.Context()), test.wantError)
		})
	}
}

func TestConnectionAddCommandRejectsBlankProjectBeforeClientCreation(t *testing.T) {
	root := t.TempDir()
	file := writeConnectionAddDefinition(t, root, "connection.yaml", `
category: CognitiveSearch
target: https://search.example.test
`)
	runtimeCreated := false
	command := newConnectionAddCommandWithDependencies(
		&azdext.ExtensionContext{},
		connectionAddDependencies{
			newRuntime: func() (*connectionAddRuntime, error) {
				runtimeCreated = true
				return nil, errors.New("runtime should not be created")
			},
			projectDir: func() (string, error) { return root, nil },
		},
	)
	command.SetOut(io.Discard)
	command.SetErr(io.Discard)
	command.SetArgs([]string{"search", "--file", file, "--project", " "})

	err := command.ExecuteContext(t.Context())
	require.ErrorContains(t, err, "--project must name")
	assert.False(t, runtimeCreated)
}

func TestConnectionAddCommandSurfacesOutputWriterFailure(t *testing.T) {
	root := t.TempDir()
	file := writeConnectionAddDefinition(t, root, "connection.yaml", `
category: CognitiveSearch
target: https://search.example.test
`)
	client := newConnectionAddProjectClientStub()
	client.services["foundry"] = map[string]any{"host": aiProjectServiceHost}
	command := newConnectionAddCommandWithDependencies(
		&azdext.ExtensionContext{},
		connectionAddDependencies{
			newRuntime: func() (*connectionAddRuntime, error) {
				return &connectionAddRuntime{project: client, close: func() {}}, nil
			},
			projectDir: func() (string, error) { return root, nil },
		},
	)
	command.SetOut(connectionAddFailingWriter{})
	command.SetArgs([]string{"search", "--file", file})

	require.ErrorContains(t, command.ExecuteContext(t.Context()), "output writer failed")
	require.Len(t, client.addRequests, 1)
	assert.Equal(t, []string{"add"}, client.mutationCalls)
}

func TestWriteConnectionAddJSONUsesEmptyEnvironmentArray(t *testing.T) {
	plan := &connectionAddPlan{
		serviceName: "search",
		mutation:    "added",
		ref:         "./connections/search.yaml",
		projectKey:  "foundry",
	}
	var output bytes.Buffer

	require.NoError(t, writeConnectionAddJSON(&output, plan))
	assert.Equal(
		t,
		"{\"name\":\"search\",\"host\":\"azure.ai.connection\",\"mutation\":\"added\","+
			"\"ref\":\"./connections/search.yaml\",\"project\":\"foundry\",\"environmentVariables\":[]}\n",
		output.String(),
	)
}

func TestWriteConnectionAddHumanUsesInjectedWriter(t *testing.T) {
	plan := &connectionAddPlan{
		serviceName:          "search",
		mutation:             "updated",
		ref:                  "./connections/search.yaml",
		projectKey:           "foundry",
		environmentVariables: []string{"SEARCH_KEY"},
	}
	var output bytes.Buffer

	require.NoError(t, writeConnectionAddHuman(&output, plan))
	assert.Equal(
		t,
		"Updated local Connection service \"search\" for Foundry Project service \"foundry\".\n"+
			"Definition remains in ./connections/search.yaml.\n"+
			"Required environment variable SEARCH_KEY: azd env set SEARCH_KEY <value>\n"+
			"Connection values are not resolved by this command; deployment validates them.\n"+
			"Run `azd deploy search` or `azd up` to apply this Connection.\n",
		output.String(),
	)
	assert.NotContains(t, output.String(), "private-value")
}

type connectionAddFailingWriter struct{}

func (connectionAddFailingWriter) Write([]byte) (int, error) {
	return 0, errors.New("output writer failed")
}
