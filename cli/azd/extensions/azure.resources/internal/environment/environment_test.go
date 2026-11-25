// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package environment

import (
	"context"
	"net"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type environmentServer struct {
	azdext.UnimplementedEnvironmentServiceServer
	environment  *azdext.Environment
	err          error
	requests     []*azdext.GetEnvironmentRequest
	currentCalls int
}

func (s *environmentServer) Get(
	_ context.Context, request *azdext.GetEnvironmentRequest,
) (*azdext.EnvironmentResponse, error) {
	s.requests = append(s.requests, request)
	return &azdext.EnvironmentResponse{Environment: s.environment}, s.err
}

func (s *environmentServer) GetCurrent(
	context.Context, *azdext.EmptyRequest,
) (*azdext.EnvironmentResponse, error) {
	s.currentCalls++
	return &azdext.EnvironmentResponse{Environment: s.environment}, s.err
}

func newTestClient(t *testing.T, environment *environmentServer) *azdext.AzdClient {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	server := grpc.NewServer()
	azdext.RegisterEnvironmentServiceServer(server, environment)
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		_ = server.Serve(listener)
	}()
	t.Cleanup(func() {
		server.Stop()
		<-finished
	})
	client, err := azdext.NewAzdClient(azdext.WithAddress(listener.Addr().String()))
	require.NoError(t, err)
	t.Cleanup(client.Close)
	return client
}

func TestResolveNameValidatesSelectedEnvironment(t *testing.T) {
	for _, test := range []struct {
		name         string
		selected     string
		envName      string
		wantSelected string
		environment  *azdext.Environment
		err          error
		wantError    string
		wantCode     codes.Code
	}{
		{name: "explicit wins", selected: "staging", envName: "dev",
			wantSelected: "staging", environment: &azdext.Environment{Name: "staging"}},
		{name: "environment variable", envName: "prod",
			wantSelected: "prod", environment: &azdext.Environment{Name: "prod"}},
		{name: "missing explicit environment", selected: "missing", envName: "dev", wantSelected: "missing",
			err: status.Error(codes.NotFound, "not found"), wantError: `getting environment "missing"`,
			wantCode: codes.NotFound},
		{name: "missing environment variable target", envName: "missing", wantSelected: "missing",
			err: status.Error(codes.NotFound, "not found"), wantError: `getting environment "missing"`,
			wantCode: codes.NotFound},
		{name: "empty response", selected: "staging", wantSelected: "staging", wantError: "returned no name"},
		{name: "access denied", selected: "staging", wantSelected: "staging",
			err: status.Error(codes.PermissionDenied, "access denied"), wantError: "access denied",
			wantCode: codes.PermissionDenied},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("AZD_ENVIRONMENT", test.envName)
			host := &environmentServer{environment: test.environment, err: test.err}
			client := newTestClient(t, host)
			name, err := ResolveName(t.Context(), client.Environment(), test.selected)
			if test.wantError != "" {
				require.ErrorContains(t, err, test.wantError)
				require.Empty(t, name)
			} else {
				require.NoError(t, err)
				require.Equal(t, test.wantSelected, name)
			}
			require.Zero(t, host.currentCalls, "invalid selections must not fall back to the default")
			require.Len(t, host.requests, 1)
			require.Equal(t, test.wantSelected, host.requests[0].Name)
			if test.wantCode != codes.OK {
				require.Equal(t, test.wantCode, status.Code(err))
			}
		})
	}
}

func TestResolveNameFallsBackToHost(t *testing.T) {
	t.Setenv("AZD_ENVIRONMENT", "")
	for _, test := range []struct {
		name        string
		environment *azdext.Environment
		err         error
		wantName    string
		wantError   string
		wantCode    codes.Code
	}{
		{name: "default", environment: &azdext.Environment{Name: "dev"}, wantName: "dev"},
		{name: "empty name", environment: &azdext.Environment{}, wantError: "run 'azd env new'"},
		{name: "missing environment", wantError: "run 'azd env new'"},
		{name: "not found", err: status.Error(codes.NotFound, "missing default"),
			wantError: "run 'azd env new'", wantCode: codes.NotFound},
		{name: "host failure", err: status.Error(codes.PermissionDenied, "access denied"),
			wantError: "access denied", wantCode: codes.PermissionDenied},
	} {
		t.Run(test.name, func(t *testing.T) {
			host := &environmentServer{
				environment: test.environment, err: test.err,
			}
			client := newTestClient(t, host)

			name, err := ResolveName(t.Context(), client.Environment(), "")
			require.Equal(t, test.wantName, name)
			if test.wantError != "" {
				require.ErrorContains(t, err, test.wantError)
			} else {
				require.NoError(t, err)
			}
			if test.wantCode != codes.OK {
				require.Equal(t, test.wantCode, status.Code(err))
			}
			require.Equal(t, 1, host.currentCalls)
			require.Empty(t, host.requests, "GetCurrent already resolves the default environment")
		})
	}
}
