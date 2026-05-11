// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package doctor

import (
	"context"
	"errors"
	"net"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// ---- fake gRPC servers (Project + Environment) ----

type fakeProjectServer struct {
	azdext.UnimplementedProjectServiceServer
	resp *azdext.GetProjectResponse
	err  error
}

func (s *fakeProjectServer) Get(
	context.Context, *azdext.EmptyRequest,
) (*azdext.GetProjectResponse, error) {
	if s.err != nil {
		return nil, s.err
	}
	return s.resp, nil
}

type fakeEnvironmentServer struct {
	azdext.UnimplementedEnvironmentServiceServer
	resp *azdext.EnvironmentResponse
	err  error
}

func (s *fakeEnvironmentServer) GetCurrent(
	context.Context, *azdext.EmptyRequest,
) (*azdext.EnvironmentResponse, error) {
	if s.err != nil {
		return nil, s.err
	}
	return s.resp, nil
}

// newTestAzdClient spins up an in-process gRPC server with the supplied
// Project + Environment server stubs and returns a client wired to its
// address. The server, listener, and client are all torn down via
// t.Cleanup. Pattern mirrors `init_foundry_resources_helpers_test.go`'s
// `newTestAzdClient` — kept local to the doctor package so the doctor
// has no cross-package test-only imports.
func newTestAzdClient(
	t *testing.T,
	projectServer *fakeProjectServer,
	envServer *fakeEnvironmentServer,
) *azdext.AzdClient {
	t.Helper()

	grpcServer := grpc.NewServer()
	azdext.RegisterProjectServiceServer(grpcServer, projectServer)
	azdext.RegisterEnvironmentServiceServer(grpcServer, envServer)

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	serveErr := make(chan error, 1)
	go func() {
		if err := grpcServer.Serve(listener); err != nil {
			serveErr <- err
		}
	}()

	t.Cleanup(func() {
		grpcServer.Stop()
		_ = listener.Close()
		select {
		case err := <-serveErr:
			require.ErrorIs(t, err, grpc.ErrServerStopped)
		default:
		}
	})

	azdClient, err := azdext.NewAzdClient(azdext.WithAddress(listener.Addr().String()))
	require.NoError(t, err)
	t.Cleanup(func() { azdClient.Close() })

	return azdClient
}

// ---- Check `local.grpc-extension` ----

func TestCheckGRPCAndVersion_NoClient_Fails(t *testing.T) {
	t.Parallel()

	check := newCheckGRPCAndVersion(Dependencies{
		AzdClient:    nil,
		AzdClientErr: errors.New("dial tcp 127.0.0.1:0: connect: connection refused"),
	})
	got := check.Fn(t.Context(), Options{}, nil)

	require.Equal(t, StatusFail, got.Status)
	require.Contains(t, got.Message, "gRPC channel to azd unavailable")
	require.Contains(t, got.Message, "connection refused")
	require.NotEmpty(t, got.Suggestion)
}

func TestCheckGRPCAndVersion_NoClient_NilErr_StillFails(t *testing.T) {
	t.Parallel()

	check := newCheckGRPCAndVersion(Dependencies{AzdClient: nil, AzdClientErr: nil})
	got := check.Fn(t.Context(), Options{}, nil)

	require.Equal(t, StatusFail, got.Status)
	require.Equal(t, "gRPC channel to azd is unavailable", got.Message)
}

func TestCheckGRPCAndVersion_DevBuild_Passes(t *testing.T) {
	t.Parallel()

	client := newTestAzdClient(t, &fakeProjectServer{}, &fakeEnvironmentServer{})

	for _, ver := range []string{"", "dev", "   "} {
		check := newCheckGRPCAndVersion(Dependencies{
			AzdClient:        client,
			ExtensionVersion: ver,
		})
		got := check.Fn(t.Context(), Options{}, nil)
		require.Equal(t, StatusPass, got.Status, "ver=%q", ver)
		require.Empty(t, got.Suggestion, "dev/empty builds should not nag")
	}
}

func TestCheckGRPCAndVersion_BelowFloor_Warns(t *testing.T) {
	t.Parallel()

	client := newTestAzdClient(t, &fakeProjectServer{}, &fakeEnvironmentServer{})

	check := newCheckGRPCAndVersion(Dependencies{
		AzdClient:        client,
		ExtensionVersion: "0.1.26-preview",
	})
	got := check.Fn(t.Context(), Options{}, nil)

	require.Equal(t, StatusWarn, got.Status)
	require.Contains(t, got.Message, "0.1.26-preview")
	require.Contains(t, got.Message, MinNewBackendVersion)
	require.Contains(t, got.Suggestion, "azd ext upgrade azure.ai.agents")
	require.Contains(t, got.Links, "https://aka.ms/hostedagents/tsg/readme")
	require.Equal(t, "0.1.26-preview", got.Details["extensionVersion"])
	require.Equal(t, MinNewBackendVersion, got.Details["minBackendVersion"])
}

func TestCheckGRPCAndVersion_EqualFloor_Passes(t *testing.T) {
	t.Parallel()

	client := newTestAzdClient(t, &fakeProjectServer{}, &fakeEnvironmentServer{})

	check := newCheckGRPCAndVersion(Dependencies{
		AzdClient:        client,
		ExtensionVersion: MinNewBackendVersion,
	})
	got := check.Fn(t.Context(), Options{}, nil)

	require.Equal(t, StatusPass, got.Status)
	require.Empty(t, got.Suggestion)
}

func TestCheckGRPCAndVersion_AboveFloor_Passes(t *testing.T) {
	t.Parallel()

	client := newTestAzdClient(t, &fakeProjectServer{}, &fakeEnvironmentServer{})

	check := newCheckGRPCAndVersion(Dependencies{
		AzdClient:        client,
		ExtensionVersion: "0.2.0",
	})
	got := check.Fn(t.Context(), Options{}, nil)

	require.Equal(t, StatusPass, got.Status)
}

// ---- Check `local.azure-yaml` ----

func TestCheckProjectConfig_NoClient_Skips(t *testing.T) {
	t.Parallel()

	check := newCheckProjectConfig(Dependencies{AzdClient: nil})
	got := check.Fn(t.Context(), Options{}, nil)

	require.Equal(t, StatusSkip, got.Status)
	require.Contains(t, got.Message, "azd extension not reachable")
}

func TestCheckProjectConfig_GrpcError_Fails(t *testing.T) {
	t.Parallel()

	client := newTestAzdClient(t,
		&fakeProjectServer{err: status.Error(codes.NotFound, "no project")},
		&fakeEnvironmentServer{},
	)
	check := newCheckProjectConfig(Dependencies{AzdClient: client})
	got := check.Fn(t.Context(), Options{}, nil)

	require.Equal(t, StatusFail, got.Status)
	require.Contains(t, got.Message, "failed to get project config")
	require.Contains(t, got.Suggestion, "azd init")
}

func TestCheckProjectConfig_NilProject_Fails(t *testing.T) {
	t.Parallel()

	client := newTestAzdClient(t,
		&fakeProjectServer{resp: &azdext.GetProjectResponse{Project: nil}},
		&fakeEnvironmentServer{},
	)
	check := newCheckProjectConfig(Dependencies{AzdClient: client})
	got := check.Fn(t.Context(), Options{}, nil)

	require.Equal(t, StatusFail, got.Status)
	require.Contains(t, got.Message, "is there an azure.yaml?")
	require.Contains(t, got.Suggestion, "azd init")
}

func TestCheckProjectConfig_Pass(t *testing.T) {
	t.Parallel()

	client := newTestAzdClient(t,
		&fakeProjectServer{
			resp: &azdext.GetProjectResponse{
				Project: &azdext.ProjectConfig{Name: "my-agent", Path: "/abs/path"},
			},
		},
		&fakeEnvironmentServer{},
	)
	check := newCheckProjectConfig(Dependencies{AzdClient: client})
	got := check.Fn(t.Context(), Options{}, nil)

	require.Equal(t, StatusPass, got.Status)
	require.Contains(t, got.Message, "my-agent")
	require.Equal(t, "/abs/path", got.Details["projectPath"])
	require.Equal(t, "my-agent", got.Details["projectName"])
}

// ---- Check `local.environment-selected` ----

func TestCheckEnvironmentSelected_NoClient_Skips(t *testing.T) {
	t.Parallel()

	check := newCheckEnvironmentSelected(Dependencies{AzdClient: nil})
	got := check.Fn(t.Context(), Options{}, nil)

	require.Equal(t, StatusSkip, got.Status)
	require.Contains(t, got.Message, "azd extension not reachable")
}

func TestCheckEnvironmentSelected_SkipsWhenProjectCheckFailed(t *testing.T) {
	t.Parallel()

	client := newTestAzdClient(t,
		&fakeProjectServer{},
		&fakeEnvironmentServer{
			resp: &azdext.EnvironmentResponse{Environment: &azdext.Environment{Name: "dev"}},
		},
	)
	check := newCheckEnvironmentSelected(Dependencies{AzdClient: client})
	prior := []Result{{ID: "local.azure-yaml", Status: StatusFail}}

	got := check.Fn(t.Context(), Options{}, prior)

	require.Equal(t, StatusSkip, got.Status)
	require.Contains(t, got.Message, "azure.yaml check failed")
}

func TestCheckEnvironmentSelected_GrpcError_Fails(t *testing.T) {
	t.Parallel()

	client := newTestAzdClient(t,
		&fakeProjectServer{},
		&fakeEnvironmentServer{err: status.Error(codes.Internal, "boom")},
	)
	check := newCheckEnvironmentSelected(Dependencies{AzdClient: client})
	got := check.Fn(t.Context(), Options{}, nil)

	require.Equal(t, StatusFail, got.Status)
	require.Contains(t, got.Message, "failed to get current environment")
	require.Contains(t, got.Suggestion, "azd env new")
	require.Contains(t, got.Suggestion, "azd env select")
}

func TestCheckEnvironmentSelected_EmptyName_Fails(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		resp *azdext.EnvironmentResponse
	}{
		{"nil response wrapper", nil},
		{"nil Environment", &azdext.EnvironmentResponse{Environment: nil}},
		{"empty Name", &azdext.EnvironmentResponse{Environment: &azdext.Environment{Name: ""}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			client := newTestAzdClient(t,
				&fakeProjectServer{},
				&fakeEnvironmentServer{resp: tc.resp},
			)
			check := newCheckEnvironmentSelected(Dependencies{AzdClient: client})
			got := check.Fn(t.Context(), Options{}, nil)

			require.Equal(t, StatusFail, got.Status)
			require.Equal(t, "no azd environment is selected", got.Message)
		})
	}
}

func TestCheckEnvironmentSelected_Pass(t *testing.T) {
	t.Parallel()

	client := newTestAzdClient(t,
		&fakeProjectServer{},
		&fakeEnvironmentServer{
			resp: &azdext.EnvironmentResponse{Environment: &azdext.Environment{Name: "staging"}},
		},
	)
	check := newCheckEnvironmentSelected(Dependencies{AzdClient: client})
	got := check.Fn(t.Context(), Options{}, nil)

	require.Equal(t, StatusPass, got.Status)
	require.Contains(t, got.Message, "staging")
	require.Equal(t, "staging", got.Details["environmentName"])
}

// ---- NewLocalChecks ordering / IDs ----

func TestNewLocalChecks_OrderAndIDs(t *testing.T) {
	t.Parallel()

	checks := NewLocalChecks(Dependencies{})
	require.Len(t, checks, 3)

	want := []struct {
		id     string
		name   string
		remote bool
	}{
		{"local.grpc-extension", "azd extension reachable", false},
		{"local.azure-yaml", "azure.yaml present and parseable", false},
		{"local.environment-selected", "azd environment selected", false},
	}
	for i, w := range want {
		require.Equal(t, w.id, checks[i].ID, "index %d", i)
		require.Equal(t, w.name, checks[i].Name, "index %d", i)
		require.Equal(t, w.remote, checks[i].Remote, "index %d", i)
		require.NotNil(t, checks[i].Fn, "index %d Fn is nil", i)
	}
}

// ---- version comparator ----

func TestCompareVersions(t *testing.T) {
	t.Parallel()

	cases := []struct {
		a, b string
		want int
	}{
		{"0.1.26-preview", "0.1.27-preview", -1},
		{"0.1.27-preview", "0.1.27-preview", 0},
		{"0.1.28-preview", "0.1.27-preview", 1},
		{"v0.1.27", "0.1.27", 0},
		{"0.1.27+build.42", "0.1.27", 0},
		{"1.0.0-preview", "0.999.999-preview", 1},
		{"0.0.1", "0.1.0", -1},
		// Fail-open: malformed strings compare as equal.
		{"not-a-version", "0.1.27", 0},
		{"0.1", "0.1.27", 0},
		{"0.1.27", "not-a-version", 0},
	}
	for _, tc := range cases {
		t.Run(tc.a+"_vs_"+tc.b, func(t *testing.T) {
			t.Parallel()
			got := compareVersions(tc.a, tc.b)
			require.Equal(t, tc.want, got)
		})
	}
}

func TestParseMainVersion(t *testing.T) {
	t.Parallel()

	cases := []struct {
		in   string
		want [3]int
		ok   bool
	}{
		{"0.1.27-preview", [3]int{0, 1, 27}, true},
		{"v0.1.27", [3]int{0, 1, 27}, true},
		{"   1.2.3+build.7   ", [3]int{1, 2, 3}, true},
		{"1.2", [3]int{}, false},
		{"1.2.x", [3]int{}, false},
		{"", [3]int{}, false},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			t.Parallel()
			got, ok := parseMainVersion(tc.in)
			require.Equal(t, tc.ok, ok)
			require.Equal(t, tc.want, got)
		})
	}
}

func TestCoalesce(t *testing.T) {
	t.Parallel()

	require.Equal(t, "first", coalesce("first", "second"))
	require.Equal(t, "second", coalesce("", "second"))
	require.Equal(t, "", coalesce("", "", ""))
	require.Equal(t, "", coalesce())
}
