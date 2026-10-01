// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package vsrpc

import (
	"path/filepath"
	"sync"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"github.com/azure/azure-dev/cli/azd/pkg/ioc"
	"github.com/stretchr/testify/require"
)

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

func TestNewContainer_ChildScopesUseSessionAzdContext(t *testing.T) {
	root := ioc.NewNestedContainer(nil)
	ioc.RegisterInstance(root, root)
	root.MustRegisterScoped(func() (*azdcontext.AzdContext, error) {
		return nil, azdcontext.ErrNoProject
	})

	hostDir := t.TempDir()
	session := &serverSession{id: "test", rootContainer: root}
	c, err := session.newContainer(RequestContext{HostProjectPath: filepath.Join(hostDir, "AppHost.csproj")})
	require.NoError(t, err)

	var resolved *ioc.NestedContainer
	require.NoError(t, c.Resolve(&resolved))

	scope, err := resolved.NewScope()
	require.NoError(t, err)

	var azdCtx *azdcontext.AzdContext
	require.NoError(t, scope.Resolve(&azdCtx))
	require.Equal(t, hostDir, azdCtx.ProjectDirectory())
}
