// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

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

func TestAcquireAgentAddProjectLockUsesExternalCanonicalPath(t *testing.T) {
	cache := t.TempDir()
	t.Setenv("LOCALAPPDATA", cache)
	t.Setenv("XDG_CACHE_HOME", cache)
	t.Setenv("HOME", cache)

	projectPath := t.TempDir()
	before := snapshotAgentServiceAddProjectTree(t, projectPath)
	first, err := acquireAgentAddProjectLock(t.Context(), projectPath)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, first.Unlock()) })

	aliases := []string{projectPath + string(os.PathSeparator) + "child" + string(os.PathSeparator) + ".."}
	require.NoError(t, os.Mkdir(filepath.Join(projectPath, "child"), 0o700))
	if runtime.GOOS == "windows" {
		aliases = append(aliases, strings.ToUpper(projectPath))
	}
	for _, alias := range aliases {
		ctx, cancel := context.WithTimeout(t.Context(), 250*time.Millisecond)
		second, err := acquireAgentAddProjectLock(ctx, alias)
		cancel()
		if second != nil {
			require.NoError(t, second.Unlock())
		}
		require.ErrorIs(t, err, context.DeadlineExceeded, alias)
	}
	require.NoError(t, os.Remove(filepath.Join(projectPath, "child")))
	require.Equal(t, before, snapshotAgentServiceAddProjectTree(t, projectPath))

	otherProject := t.TempDir()
	other, err := acquireAgentAddProjectLock(t.Context(), otherProject)
	require.NoError(t, err)
	require.NoError(t, other.Unlock())

	cacheDirectory, err := os.UserCacheDir()
	require.NoError(t, err)
	files, err := os.ReadDir(filepath.Join(cacheDirectory, "azd", "agent-add-locks"))
	require.NoError(t, err)
	require.Len(t, files, 2)
	require.NoError(t, first.Unlock())
	second, err := acquireAgentAddProjectLock(t.Context(), projectPath)
	require.NoError(t, err)
	require.NoError(t, second.Unlock())
	after, err := os.ReadDir(filepath.Join(cacheDirectory, "azd", "agent-add-locks"))
	require.NoError(t, err)
	require.Equal(t, files, after)
}

func TestAcquireAgentAddProjectLockResolvesSymlinkAliases(t *testing.T) {
	cache := t.TempDir()
	t.Setenv("LOCALAPPDATA", cache)
	t.Setenv("XDG_CACHE_HOME", cache)
	t.Setenv("HOME", cache)

	projectPath := t.TempDir()
	alias := filepath.Join(t.TempDir(), "project-link")
	if err := os.Symlink(projectPath, alias); err != nil {
		t.Skipf("creating a directory symlink is not supported: %v", err)
	}
	first, err := acquireAgentAddProjectLock(t.Context(), projectPath)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, first.Unlock()) })
	ctx, cancel := context.WithTimeout(t.Context(), 250*time.Millisecond)
	defer cancel()
	second, err := acquireAgentAddProjectLock(ctx, alias)
	if second != nil {
		require.NoError(t, second.Unlock())
	}
	require.ErrorIs(t, err, context.DeadlineExceeded)
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

func TestAgentDependencyCommandsContainAdd(t *testing.T) {
	t.Parallel()

	for _, test := range []struct{ kind, host string }{
		{kind: "toolbox", host: AiToolboxHost},
		{kind: "connection", host: AiConnectionHost},
	} {
		t.Run(test.kind, func(t *testing.T) {
			t.Parallel()
			command := newAgentDependencyCommand(&azdext.ExtensionContext{}, test.kind, test.host)
			assert.Equal(t, test.kind, command.Name())
			add, remaining, err := command.Find([]string{"add"})
			require.NoError(t, err)
			require.Empty(t, remaining)
			assert.Equal(t, "add", add.Name())
			assert.Equal(t, "add <service>", add.Use)
			assert.Equal(t, command, add.Parent())
			require.NotNil(t, add.Flags().Lookup("agent"))
			require.NoError(t, add.ParseFlags([]string{"--agent", "research-agent"}))
			agentName, err := add.Flags().GetString("agent")
			require.NoError(t, err)
			assert.Equal(t, "research-agent", agentName)
			require.NoError(t, add.Args(add, []string{"dependency"}))
			require.Error(t, add.Args(add, nil))
			require.Error(t, add.Args(add, []string{"first", "second"}))
		})
	}
}
