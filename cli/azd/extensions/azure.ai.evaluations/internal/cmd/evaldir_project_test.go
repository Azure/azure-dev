// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"errors"
	"net"
	"testing"

	"azureaieval/internal/project"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// azd's own sentinel for being run outside a project, as it reaches us. Spelled
// out rather than imported because the resolver keeps it unexported on purpose:
// a rename there should fail this test, which is the point of pinning it.
const azdOutsideAProject = "no project exists; to create a new project, run `azd init`"

// testProjectServer answers Get with whatever the test wants it to fail with.
type testProjectServer struct {
	azdext.UnimplementedProjectServiceServer
	err error
}

func (s *testProjectServer) Get(
	_ context.Context, _ *azdext.EmptyRequest,
) (*azdext.GetProjectResponse, error) {
	return nil, s.err
}

// newTestProjectClient serves the project over gRPC the way azd does, so the
// error travels the wire and arrives wearing the status code it really would.
// Classifying it is the whole subject here, and a hand-built error would beg
// the question.
func newTestProjectClient(t *testing.T, err error) *azdext.AzdClient {
	t.Helper()

	server := grpc.NewServer()
	azdext.RegisterProjectServiceServer(server, &testProjectServer{err: err})

	listener, netErr := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, netErr)
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() {
		server.Stop()
		_ = listener.Close()
	})

	client, clientErr := azdext.NewAzdClient(azdext.WithAddress(listener.Addr().String()))
	require.NoError(t, clientErr)
	t.Cleanup(func() { client.Close() })

	return client
}

// Where the configuration lives came from azd, and every way of failing to ask
// it was read as "there is no project" -- which sends the cascade to its
// default. Inside a project that is the wrong directory: `init` writes a second
// configuration under `evals` beside the caller while azure.yaml's $ref, and
// therefore the deploy, goes on pointing at the first.
//
// So the two have to be told apart, and the split is not "did it error" but
// "was it an answer".
func TestProjectEvalLocationSeparatesNoProjectFromNoAnswer(t *testing.T) {
	cases := []struct {
		name    string
		err     error
		wantErr bool
	}{
		{
			name: "outside a project is an answer",
			err:  errors.New(azdOutsideAProject),
		},
		{
			name: "no daemon is an answer too: nothing spawned us, so `evals` " +
				"beside the caller is right",
			err: status.Error(codes.Unavailable, "no daemon"),
		},
		{
			name:    "a denial is not an answer about the project",
			err:     status.Error(codes.PermissionDenied, "not allowed"),
			wantErr: true,
		},
		{
			name:    "and neither is a fault",
			err:     errors.New("failed to load project state"),
			wantErr: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := projectEvalLocation(
				t.Context(), newTestProjectClient(t, tc.err))

			if tc.wantErr {
				require.Error(t, err, "azd failing to answer must not read as a fact")
				assert.Empty(t, got)
				return
			}
			require.NoError(t, err)
			assert.Empty(t, got, "no project declares anything, so the cascade carries on")
		})
	}
}

// The reason the distinction matters, through the cascade a command actually
// calls: the failure has to reach the caller instead of quietly becoming a
// directory nothing else uses.
func TestEvalDirDoesNotDefaultWhenAzdCouldNotAnswer(t *testing.T) {
	ec := &evalContext{
		azdClient: newTestProjectClient(t, status.Error(codes.PermissionDenied, "not allowed")),
	}

	got, err := ec.evalDir(t.Context(), "")

	require.Error(t, err)
	assert.NotEqual(t, project.DefaultEvalDir, got,
		"defaulting here is how a second configuration gets written")

	// A --path that was given is still the answer on its own: a caller who has
	// already said where to look should not be stopped by azd.
	got, err = ec.evalDir(t.Context(), "quality")
	require.NoError(t, err)
	assert.Equal(t, "quality", got)
}
