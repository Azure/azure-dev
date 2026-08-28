// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"testing"

	"azureaiagent/internal/exterrors"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAddAgentServiceDependency(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		dependencyType string
		dependencyHost string
		dependencyName string
	}{
		{
			name:           "toolbox",
			dependencyType: "toolbox",
			dependencyHost: AiToolboxHost,
			dependencyName: "support-tools",
		},
		{
			name:           "connection",
			dependencyType: "connection",
			dependencyHost: AiConnectionHost,
			dependencyName: "search-connection",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			server := &recordingProjectServer{existing: map[string]*azdext.ServiceConfig{
				"research-agent": {Name: "research-agent", Host: AiAgentHost, Uses: []string{"ai-project"}},
				test.dependencyName: {
					Name: test.dependencyName, Host: test.dependencyHost,
				},
			}}
			client := newProjectRecorderClient(t, server)

			added, err := addAgentServiceDependency(
				t.Context(), client, "research-agent", test.dependencyName,
				test.dependencyType, test.dependencyHost,
			)
			require.NoError(t, err)
			assert.True(t, added)
			assert.Equal(t, []string{"ai-project", test.dependencyName}, server.uses["research-agent"])
		})
	}
}

func TestAddAgentServiceDependencyIsIdempotent(t *testing.T) {
	t.Parallel()

	server := &recordingProjectServer{existing: map[string]*azdext.ServiceConfig{
		"research-agent": {
			Name: "research-agent", Host: AiAgentHost, Uses: []string{"ai-project", "support-tools"},
		},
		"support-tools": {Name: "support-tools", Host: AiToolboxHost},
	}}
	client := newProjectRecorderClient(t, server)

	added, err := addAgentServiceDependency(
		t.Context(), client, "research-agent", "support-tools", "toolbox", AiToolboxHost,
	)
	require.NoError(t, err)
	assert.False(t, added)
	assert.Empty(t, server.uses)
}

func TestAddAgentServiceDependencyRejectsCycle(t *testing.T) {
	t.Parallel()

	server := &recordingProjectServer{existing: map[string]*azdext.ServiceConfig{
		"research-agent": {Name: "research-agent", Host: AiAgentHost, Uses: []string{"ai-project"}},
		"support-tools":  {Name: "support-tools", Host: AiToolboxHost, Uses: []string{"shared", "research-agent"}},
		"shared":         {Name: "shared", Host: AiConnectionHost},
	}}
	client := newProjectRecorderClient(t, server)

	_, err := addAgentServiceDependency(
		t.Context(), client, "research-agent", "support-tools", "toolbox", AiToolboxHost,
	)
	var localErr *azdext.LocalError
	require.ErrorAs(t, err, &localErr)
	assert.Equal(t, exterrors.CodeInvalidServiceConfig, localErr.Code)
	assert.Contains(t, localErr.Message, "dependency cycle")
	assert.Empty(t, server.uses)
}

func TestAddAgentServiceDependencyValidatesServices(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		agent      string
		dependency string
		services   map[string]*azdext.ServiceConfig
		code       string
	}{
		{
			name:       "agent is required",
			dependency: "support-tools",
			code:       exterrors.CodeInvalidParameter,
		},
		{
			name:  "dependency is required",
			agent: "research-agent",
			services: map[string]*azdext.ServiceConfig{
				"research-agent": {Name: "research-agent", Host: AiAgentHost},
			},
			code: exterrors.CodeInvalidParameter,
		},
		{
			name:       "agent is missing",
			agent:      "missing-agent",
			dependency: "support-tools",
			services: map[string]*azdext.ServiceConfig{
				"support-tools": {Name: "support-tools", Host: AiToolboxHost},
			},
			code: exterrors.CodeInvalidServiceConfig,
		},
		{
			name:       "dependency is missing",
			agent:      "research-agent",
			dependency: "missing-tools",
			services: map[string]*azdext.ServiceConfig{
				"research-agent": {Name: "research-agent", Host: AiAgentHost},
			},
			code: exterrors.CodeInvalidServiceConfig,
		},
		{
			name:       "agent has wrong host",
			agent:      "research-agent",
			dependency: "support-tools",
			services: map[string]*azdext.ServiceConfig{
				"research-agent": {Name: "research-agent", Host: AiToolboxHost},
				"support-tools":  {Name: "support-tools", Host: AiToolboxHost},
			},
			code: exterrors.CodeUnsupportedHost,
		},
		{
			name:       "dependency has wrong host",
			agent:      "research-agent",
			dependency: "support-tools",
			services: map[string]*azdext.ServiceConfig{
				"research-agent": {Name: "research-agent", Host: AiAgentHost},
				"support-tools":  {Name: "support-tools", Host: AiConnectionHost},
			},
			code: exterrors.CodeUnsupportedHost,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			server := &recordingProjectServer{existing: test.services}
			client := newProjectRecorderClient(t, server)

			_, err := addAgentServiceDependency(
				t.Context(), client, test.agent, test.dependency, "toolbox", AiToolboxHost,
			)
			var localErr *azdext.LocalError
			require.ErrorAs(t, err, &localErr)
			assert.Equal(t, test.code, localErr.Code)
		})
	}
}

func TestAgentAddContainsTypedDependencies(t *testing.T) {
	t.Parallel()

	command := newAgentAddCommand(&azdext.ExtensionContext{})
	toolbox, _, err := command.Find([]string{"toolbox"})
	require.NoError(t, err)
	assert.Equal(t, "toolbox", toolbox.Name())
	connection, _, err := command.Find([]string{"connection"})
	require.NoError(t, err)
	assert.Equal(t, "connection", connection.Name())
}
