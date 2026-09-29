// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// The reconciliation section is read, merged and written as three separate
// calls, so concurrent service deploys have to take a lock. projectRoot decided
// whether there was one to take, and answered every failure to reach azd with
// "no project" -- which is also how it says "nothing to lock". A transient
// Project().Get failure therefore handed back a no-op lock and turned the next
// write into a lost update, dropping another service's state.
func TestProjectRootDoesNotReadAFaultAsThereBeingNoProject(t *testing.T) {
	ec := &evalContext{
		azdClient: newTestProjectClient(t, status.Error(codes.PermissionDenied, "not allowed")),
	}

	_, err := ec.projectRoot(t.Context())
	require.Error(t, err, "azd failing to answer is not an answer about the project")

	_, err = ec.lockPrivateState(t.Context())
	assert.Error(t, err, "an unguarded write is worse than a refused one")
}

// And it must not be remembered. Caching the failure answers for the rest of
// the process, so one hiccup early on leaves every later write unlocked -- the
// same trap deployCommandName was fixed for.
func TestProjectRootDoesNotCacheAFault(t *testing.T) {
	ec := &evalContext{
		azdClient: newTestProjectClient(t, status.Error(codes.Internal, "server fault")),
	}

	_, first := ec.projectRoot(t.Context())
	require.Error(t, first)
	assert.False(t, ec.rootKnown, "a failed lookup is not a known root")

	_, second := ec.projectRoot(t.Context())
	assert.Error(t, second, "the question has to be asked again, not answered from a cache")
}

// The cases that really are "nothing to lock" still are. Running outside a
// project, or with no azd to ask, is ordinary for the atomic commands, and
// making either fatal would break every standalone invocation.
func TestProjectRootStillAnswersEmptyWhenThereIsNoProject(t *testing.T) {
	cases := []struct {
		name string
		ec   *evalContext
	}{
		{
			name: "no azd client at all",
			ec:   &evalContext{},
		},
		{
			name: "azd says there is no project",
			ec: &evalContext{
				azdClient: newTestProjectClient(t,
					status.Error(codes.Unavailable, "no daemon")),
			},
		},
		{
			// An azd whose build predates the service has nothing to say on the
			// subject. Reading that as a fault would make this extension refuse
			// to run against any azd older than the calls it makes.
			name: "azd does not carry the project service",
			ec: &evalContext{
				azdClient: newTestProjectClient(t,
					status.Error(codes.Unimplemented, "unknown service azdext.ProjectService")),
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root, err := tc.ec.projectRoot(t.Context())
			require.NoError(t, err)
			assert.Empty(t, root)

			unlock, err := tc.ec.lockPrivateState(t.Context())
			require.NoError(t, err, "there is no directory to lock in, and that is fine")
			require.NotNil(t, unlock)
			unlock()
		})
	}
}
