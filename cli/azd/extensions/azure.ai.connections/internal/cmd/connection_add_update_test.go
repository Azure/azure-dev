// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"errors"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConnectionAddCommandUpdatesExistingServiceOffline(t *testing.T) {
	root := t.TempDir()
	file := writeConnectionAddDefinition(t, root, "connections/search.yaml", `
category: CognitiveSearch
target: https://search.example.test
authType: ApiKey
credentials:
  key: ${SEARCH_KEY}
`)
	client := newConnectionAddProjectClientStub()
	client.services["foundry"] = map[string]any{"host": aiProjectServiceHost}
	client.services["search"] = map[string]any{
		"host":        aiConnectionHost,
		"category":    "OldCategory",
		"target":      "old-target",
		"authType":    "None",
		"uses":        []any{"foundry"},
		"env":         map[string]any{"KEEP": "literal"},
		"hooks":       map[string]any{"predeploy": []any{map[string]any{"run": "echo ready"}}},
		"language":    "python",
		"config":      map[string]any{"keep": "core-config"},
		"credentials": map[string]any{"key": "private-value"},
	}

	command := newConnectionAddCommandWithDependencies(
		&azdext.ExtensionContext{},
		connectionAddDependencies{
			newRuntime: func() (*connectionAddRuntime, error) {
				return &connectionAddRuntime{
					project: client,
					close:   func() {},
				}, nil
			},
			projectDir: func() (string, error) { return root, nil },
		},
	)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	command.SetOut(&stdout)
	command.SetErr(&stderr)
	command.SetArgs([]string{"search", "--file", file})

	require.NoError(t, command.ExecuteContext(t.Context()))
	assert.Empty(t, stderr.String())
	assert.Contains(t, stdout.String(), "Updated local Connection service \"search\"")
	assert.Contains(t, stdout.String(), "Required environment variable SEARCH_KEY")
	assert.NotContains(t, stdout.String(), "private-value")
	require.Empty(t, client.addRequests)
	require.Len(t, client.serviceSetRequests, 2)
	assert.Equal(t, "search", client.serviceSetRequests[0].GetServiceName())
	assert.Equal(t, []string{
		"set:$ref", "set:env", "unset:authType",
		"unset:category", "unset:credentials", "unset:target",
	}, client.mutationCalls)
}

func TestConnectionAddCommandDoesNotReportSuccessAfterPartialWrite(t *testing.T) {
	root := t.TempDir()
	file := writeConnectionAddDefinition(t, root, "connection.yaml",
		"category: Search\ntarget: ${SEARCH_ENDPOINT}\n")
	client := newConnectionAddProjectClientStub()
	client.services["foundry"] = map[string]any{"host": aiProjectServiceHost}
	client.services["search"] = map[string]any{
		"host": aiConnectionHost, "uses": []any{"foundry"},
		"category": "Search", "target": "old",
	}
	client.failAt = 2
	client.writeError = errors.New("transport interrupted")
	command := newConnectionAddCommandWithDependencies(
		&azdext.ExtensionContext{OutputFormat: "json"},
		connectionAddDependencies{
			newRuntime: func() (*connectionAddRuntime, error) {
				return &connectionAddRuntime{project: client}, nil
			},
			projectDir: func() (string, error) { return root, nil },
		},
	)
	command.SilenceUsage = true
	command.SilenceErrors = true
	var stdout, stderr bytes.Buffer
	command.SetOut(&stdout)
	command.SetErr(&stderr)
	command.SetArgs([]string{"search", "--file", file})
	err := command.ExecuteContext(t.Context())
	require.ErrorIs(t, err, client.writeError)
	assert.Contains(t, err.Error(), "changes may be partially applied")
	assert.Contains(t, err.Error(), "rerun to reconcile")
	assert.Empty(t, stdout.String())
	assert.Equal(t, []string{"set:$ref", "set:env"}, client.mutationCalls)
}
