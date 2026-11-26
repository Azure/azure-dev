// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"azureaiagent/internal/exterrors"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/require"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestInitProjectDiscoveryDoesNotScaffoldOnReadFailure(t *testing.T) {
	for _, tt := range []struct {
		name        string
		projectFile bool
		code        codes.Code
		reason      string
		scaffold    bool
	}{
		{name: "typed absent project", code: codes.Unknown, reason: "not_found", scaffold: true},
		{name: "typed read failure in an empty directory", code: codes.Unknown, reason: "read_failed"},
		{name: "legacy fresh project", code: codes.Unknown, scaffold: true},
		{name: "legacy unreadable parent manifest", projectFile: true, code: codes.Unknown},
		{name: "unavailable on empty directory", code: codes.Unavailable},
		{name: "deadline", code: codes.DeadlineExceeded},
		{name: "canceled", code: codes.Canceled},
	} {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			source := filepath.Join(root, "src")
			require.NoError(t, os.Mkdir(source, 0o700))
			t.Chdir(source)
			t.Setenv("AZD_EXEC_PROJECT_DIR", "")
			if tt.projectFile {
				require.NoError(t, os.WriteFile(filepath.Join(root, "azure.yml"), []byte("name: ["), 0o600))
			}
			host := &initScopeHost{project: helpersProjectServer{
				err: status.Error(tt.code, "project discovery failed"), getErrReason: tt.reason,
			}}
			host.start(t)
			client, err := azdext.NewAzdClient()
			require.NoError(t, err)
			t.Cleanup(client.Close)
			for _, target := range []string{"", "image-agent"} {
				action := &InitFromCodeAction{azdClient: client, flags: &initFlags{}, projectTargetDir: target}
				_, err := action.ensureProject(t.Context())
				if tt.scaffold {
					require.ErrorContains(t, err, "fixture stopped before init writes")
				} else {
					require.Equal(t, tt.code, status.Code(err))
					require.ErrorContains(t, err, "project discovery failed")
				}
			}
			require.Equal(t, tt.scaffold, host.called("/Run"))
			require.False(t, host.called("/SetValue"))
			require.NoFileExists(t, filepath.Join(source, "azure.yaml"))
		})
	}
}

func TestInitScaffoldErrorPreservesKnownWorkflowOutcome(t *testing.T) {
	t.Parallel()
	deadline := status.Error(codes.DeadlineExceeded, "workflow timed out")
	require.ErrorIs(t, initScaffoldError(deadline), deadline)
	for _, tt := range []struct {
		code codes.Code
		want string
	}{
		{codes.Canceled, exterrors.CodeCancelled},
		{codes.AlreadyExists, exterrors.CodeEnvironmentCreationFailed},
		{codes.Internal, exterrors.CodeProjectInitFailed},
	} {
		err := initScaffoldError(status.Error(tt.code, "workflow stopped"))
		local, ok := errors.AsType[*azdext.LocalError](err)
		require.True(t, ok, "%v", err)
		require.Equal(t, tt.want, local.Code)
	}
}

func TestInitSubscriptionRPCErrorPreservesAuthReasons(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		reason string
		want   string
	}{
		{azdext.AuthErrorReasonNotLoggedIn, exterrors.CodeNotLoggedIn},
		{azdext.AuthErrorReasonLoginRequired, exterrors.CodeLoginExpired},
		{"", exterrors.CodeAuthFailed},
	} {
		st, err := status.New(codes.Unauthenticated, "credential unavailable").WithDetails(&errdetails.ErrorInfo{
			Domain: azdext.AuthErrorDomain, Reason: tt.reason,
		})
		require.NoError(t, err)
		local, ok := errors.AsType[*azdext.LocalError](initSubscriptionRPCError(st.Err()))
		require.True(t, ok)
		require.Equal(t, tt.want, local.Code)
	}
	require.ErrorIs(t, initSubscriptionRPCError(context.DeadlineExceeded), context.DeadlineExceeded)
	local, ok := errors.AsType[*azdext.LocalError](initSubscriptionRPCError(
		status.Error(codes.PermissionDenied, "subscription forbidden"),
	))
	require.True(t, ok)
	require.Contains(t, local.Suggestion, "subscription")
	require.NotContains(t, local.Suggestion, "auth login")
}
