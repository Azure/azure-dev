// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"

	"azureaiagent/internal/exterrors"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
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

func TestNormalizeAgentDependencyNames(t *testing.T) {
	t.Parallel()

	agent, dependency := normalizeAgentDependencyNames(" research-agent ", " support-tools ")
	assert.Equal(t, "research-agent", agent)
	assert.Equal(t, "support-tools", dependency)
}

func TestAddAgentServiceDependencyPreservesProjectGetError(t *testing.T) {
	t.Parallel()

	server := &recordingProjectServer{
		getProjectErr: status.Error(codes.PermissionDenied, "project access denied"),
	}
	client := newProjectRecorderClient(t, server)

	_, err := addAgentServiceDependency(
		t.Context(), client, "research-agent", "support-tools", "toolbox", AiToolboxHost,
	)
	require.Error(t, err)
	assert.Equal(t, codes.PermissionDenied, status.Code(err))
	assert.Contains(t, err.Error(), "project access denied")
	_, isLocalError := errors.AsType[*azdext.LocalError](err)
	assert.False(t, isLocalError)
}

func TestAddAgentServiceDependencyReportsMissingProjectOnlyForEmptyResponse(t *testing.T) {
	t.Parallel()

	server := &recordingProjectServer{nilProject: true}
	client := newProjectRecorderClient(t, server)

	_, err := addAgentServiceDependency(
		t.Context(), client, "research-agent", "support-tools", "toolbox", AiToolboxHost,
	)
	localErr, ok := errors.AsType[*azdext.LocalError](err)
	require.True(t, ok)
	assert.Equal(t, exterrors.CodeProjectNotFound, localErr.Code)
}

func TestAddAgentServiceDependencyPreservesSetError(t *testing.T) {
	t.Parallel()

	server := &recordingProjectServer{
		projectPath: t.TempDir(),
		existing: map[string]*azdext.ServiceConfig{
			"research-agent": {Name: "research-agent", Host: AiAgentHost},
			"support-tools":  {Name: "support-tools", Host: AiToolboxHost},
		},
		setServiceConfigErr: status.Error(codes.Unauthenticated, "host authentication failed"),
	}
	client := newProjectRecorderClient(t, server)

	_, err := addAgentServiceDependency(
		t.Context(), client, "research-agent", "support-tools", "toolbox", AiToolboxHost,
	)
	require.Error(t, err)
	assert.Equal(t, codes.Unauthenticated, status.Code(err))
	assert.Contains(t, err.Error(), "host authentication failed")
	_, isLocalError := errors.AsType[*azdext.LocalError](err)
	assert.False(t, isLocalError)
}

func TestAddAgentServiceDependencySerializesConcurrentUpdates(t *testing.T) {
	t.Parallel()

	server := &recordingProjectServer{
		projectPath: t.TempDir(),
		existing: map[string]*azdext.ServiceConfig{
			"research-agent":   {Name: "research-agent", Host: AiAgentHost, Uses: []string{"ai-project"}},
			"support-tools":    {Name: "support-tools", Host: AiToolboxHost},
			"connection-tools": {Name: "connection-tools", Host: AiToolboxHost},
		},
	}
	client := newProjectRecorderClient(t, server)
	dependencies := []string{"support-tools", "connection-tools"}
	start := make(chan struct{})
	errs := make(chan error, len(dependencies))
	var wg sync.WaitGroup
	for _, dependency := range dependencies {
		wg.Go(func() {
			<-start
			_, err := addAgentServiceDependency(
				t.Context(), client, "research-agent", dependency, "toolbox", AiToolboxHost,
			)
			errs <- err
		})
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}

	server.mu.Lock()
	uses := slices.Clone(server.existing["research-agent"].GetUses())
	server.mu.Unlock()
	assert.ElementsMatch(t, []string{"ai-project", "support-tools", "connection-tools"}, uses)
}

func TestAcquireAgentAddProjectLockPreservesCancellation(t *testing.T) {
	t.Parallel()

	projectPath := t.TempDir()
	projectLock, err := acquireAgentAddProjectLock(t.Context(), projectPath)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, projectLock.Unlock()) })

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err = acquireAgentAddProjectLock(ctx, projectPath)
	require.ErrorIs(t, err, context.Canceled)
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
