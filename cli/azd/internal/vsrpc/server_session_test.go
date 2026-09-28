// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package vsrpc

import (
	"path/filepath"
	"sync"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/ioc"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockinput"
	"github.com/stretchr/testify/require"
)

func TestNewContainer_IsolatesRequests(t *testing.T) {
	t.Parallel()

	type requestService struct {
		projectContext *azdcontext.AzdContext
		console        input.Console
	}

	projectRoot := t.TempDir()
	rootContainer := ioc.NewNestedContainer(nil)
	rootContext := azdcontext.NewAzdContextWithDirectory(projectRoot)
	rootConsole := mockinput.NewMockConsole()

	ioc.RegisterInstance(rootContainer, rootContext)
	ioc.RegisterInstance[input.Console](rootContainer, rootConsole)
	rootContainer.MustRegisterSingleton(func(projectContext *azdcontext.AzdContext, console input.Console) *requestService {
		return &requestService{projectContext: projectContext, console: console}
	})

	var rootService *requestService
	require.NoError(t, rootContainer.Resolve(&rootService))
	require.NotNil(t, rootService)

	server := NewServer(rootContainer)
	sessionInfo, err := newServerService(server, nil).InitializeAsync(t.Context(), projectRoot, InitializeServerOptions{})
	require.NoError(t, err)
	session, err := server.validateSession(*sessionInfo)
	require.NoError(t, err)

	var services []*requestService

	// create two "requests" - we'll check, after this, that they are properly creating their
	// own instances of types that are scoped.
	for _, name := range []string{"first", "second"} {
		projectDir := filepath.Join(projectRoot, name)
		hostProjectPath := filepath.Join(projectDir, "apphost.csproj")
		require.NoError(t, createAppHost(hostProjectPath))
		require.NoError(t, createProject(projectDir, "apphost.csproj"))

		perRequestContainer, err := session.newContainer(RequestContext{
			Session:         *sessionInfo,
			HostProjectPath: hostProjectPath,
		})
		require.NoError(t, err)

		var service *requestService
		require.NoError(t, perRequestContainer.Resolve(&service))
		require.Equal(t, projectDir, service.projectContext.ProjectDirectory())
		require.NotSame(t, rootService, service)
		require.Same(t, perRequestContainer.outWriter, service.console.Handles().Stdout)

		var requestConsole input.Console
		require.NoError(t, perRequestContainer.Resolve(&requestConsole))
		require.Same(t, requestConsole, service.console)

		var repeated *requestService
		require.NoError(t, perRequestContainer.Resolve(&repeated))
		require.Same(t, service, repeated)
		services = append(services, service)
	}

	// scoped instances are not shared across different scopes.. :)
	require.NotSame(t, services[0], services[1])
	require.NotSame(t, services[0].projectContext, services[1].projectContext)
	require.NotSame(t, services[0].console, services[1].console)

	// and singleton instances are shared
	var rootAgain *requestService
	require.NoError(t, rootContainer.Resolve(&rootAgain))
	require.Same(t, rootService, rootAgain)
	require.Same(t, rootContext, rootAgain.projectContext)
	require.Same(t, rootConsole, rootAgain.console)
}

func TestNewSession_CreatesUniqueIDs(t *testing.T) {
	s := newTestServer()

	id1, sess1, err1 := s.newSession()
	require.NoError(t, err1)
	require.NotEmpty(t, id1)
	require.NotNil(t, sess1)

	id2, sess2, err2 := s.newSession()
	require.NoError(t, err2)
	require.NotEmpty(t, id2)
	require.NotNil(t, sess2)

	require.NotEqual(t, id1, id2, "each session should have a unique ID")
}

func TestNewSession_RegistersInMap(t *testing.T) {
	s := newTestServer()

	id, session, err := s.newSession()
	require.NoError(t, err)

	// sessionFromId should find it
	found, ok := s.sessionFromId(id)
	require.True(t, ok)
	require.Same(t, session, found, "should return the exact same session pointer")
}

func TestSessionFromId_NotFound(t *testing.T) {
	s := newTestServer()

	_, ok := s.sessionFromId("nonexistent-id")
	require.False(t, ok)
}

func TestSessionFromId_ConcurrentAccess(t *testing.T) {
	s := newTestServer()
	const goroutines = 50

	ids := make([]string, goroutines)
	errs := make([]error, goroutines)
	var wg sync.WaitGroup
	wg.Add(goroutines)

	for i := range goroutines {
		go func(idx int) {
			defer wg.Done()
			id, _, err := s.newSession()
			ids[idx] = id
			errs[idx] = err
		}(i)
	}
	wg.Wait()

	// Verify all goroutines succeeded, then check uniqueness
	for i, err := range errs {
		require.NoErrorf(t, err, "goroutine %d failed", i)
	}

	seen := make(map[string]bool)
	for _, id := range ids {
		require.NotEmpty(t, id)
		require.False(t, seen[id], "duplicate session ID detected")
		seen[id] = true

		_, ok := s.sessionFromId(id)
		require.True(t, ok)
	}
}

func TestValidateSession_ValidId(t *testing.T) {
	s := newTestServer()

	id, _, err := s.newSession()
	require.NoError(t, err)

	ss, err := s.validateSession(Session{Id: id})
	require.NoError(t, err)
	require.NotNil(t, ss)
	require.Equal(t, id, ss.id, "session id should be set on the serverSession")
}
