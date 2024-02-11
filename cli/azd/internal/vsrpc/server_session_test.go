// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package vsrpc

import (
	"context"
	"os"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/ioc"
	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/dotnet"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockexec"
	"github.com/stretchr/testify/require"
	"go.lsp.dev/jsonrpc2"
)

func TestNewSession(t *testing.T) {
	t.Parallel()

	rootContainer := ioc.NewNestedContainer(nil)
	s := NewServer(rootContainer)

	id, session, err := s.newSession()
	require.NoError(t, err)
	require.NotEmpty(t, id)
	require.NotNil(t, session)

	// Each session should get its own id.
	id2, _, err := s.newSession()
	require.NoError(t, err)
	require.NotEmpty(t, id2)
	require.NotEqual(t, id, id2)
}

func TestValidateSession(t *testing.T) {
	t.Parallel()

	rootContainer := ioc.NewNestedContainer(nil)
	s := NewServer(rootContainer)

	// Trying to use an empty session ID returns a RPC Error.
	_, err := s.validateSession(context.Background(), Session{Id: ""})
	require.Error(t, err)
	require.IsType(t, (*jsonrpc2.Error)(nil), err)

	// And so does trying to use some ID that doesn't exist:
	_, err = s.validateSession(context.Background(), Session{Id: "unknownSessionId"})
	require.Error(t, err)
	require.IsType(t, (*jsonrpc2.Error)(nil), err)

	id, session, err := s.newSession()
	require.NoError(t, err)
	require.NotEmpty(t, id)
	require.NotNil(t, session)

	// Recovering a session with an ID returns the initial session object.
	validatedSession, err := s.validateSession(context.Background(), Session{Id: id})
	require.NoError(t, err)
	require.NotNil(t, validatedSession)

	require.Same(t, session, validatedSession)

	// Recovering a session with the same ID twice returns the same session object.
	validatedSession2, err := s.validateSession(context.Background(), Session{Id: id})
	require.NoError(t, err)
	require.NotNil(t, validatedSession2)

	require.Same(t, validatedSession, validatedSession2)
}

const emptyManfiest = `{ "resources": {} }`

func TestSessionManifestCache(t *testing.T) {
	t.Parallel()

	runner := mockexec.NewMockCommandRunner()
	dotnetCli := dotnet.NewDotNetCli(runner)

	rootContainer := ioc.NewNestedContainer(nil)
	s := NewServer(rootContainer)
	_, ss, _ := s.newSession()

	numCalls := 0

	runner.When(func(args exec.RunArgs, command string) bool {
		return args.Cmd == "dotnet" &&
			args.Args[3] == "--publisher" &&
			args.Args[4] == "manifest" &&
			args.Args[5] == "--output-path"
	}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
		numCalls++
		err := os.WriteFile(args.Args[6], []byte(emptyManfiest), osutil.PermissionFileOwnerOnly)
		require.NoError(t, err)
		return exec.NewRunResult(0, "", ""), nil
	})

	// The manfiest cache is segmented by app host path, so we execpt two calls to readManifest to actually
	// only call the AppHost project once to generate the manifest.
	manifest, err := ss.readManifest(context.Background(), "/path/to/apphost.csproj", dotnetCli)
	require.NoError(t, err)

	manifest2, err := ss.readManifest(context.Background(), "/path/to/apphost.csproj", dotnetCli)
	require.NoError(t, err)

	require.Same(t, manifest, manifest2)
	require.Equal(t, 1, numCalls)

	// But passing in another path means we have to call `dotnet` again and we get a different manifest object
	manifest3, err := ss.readManifest(context.Background(), "/a/different/path/to/apphost.csproj", dotnetCli)
	require.NoError(t, err)

	require.Equal(t, 2, numCalls)
	require.NotSame(t, manifest, manifest3)
}
