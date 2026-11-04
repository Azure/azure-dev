// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"os"
	"strings"
	"sync"
	"testing"

	"azure.ai.content-understanding/internal/config"
	"azure.ai.content-understanding/internal/cuclient"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type userConfigServer struct {
	azdext.UnimplementedUserConfigServiceServer
	mu       sync.Mutex
	settings map[string]json.RawMessage
	requests []string
	err      error
}

func (s *userConfigServer) Get(
	_ context.Context, request *azdext.GetUserConfigRequest,
) (*azdext.GetUserConfigResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.requests = append(s.requests, "get:"+request.Path)
	if s.err != nil {
		return nil, s.err
	}
	if request.Path != config.UserConfigPath {
		return nil, status.Error(codes.InvalidArgument, "unexpected configuration namespace")
	}
	if s.settings == nil {
		return &azdext.GetUserConfigResponse{}, nil
	}
	body, err := json.Marshal(s.settings)
	return &azdext.GetUserConfigResponse{Found: true, Value: body}, err
}

func (s *userConfigServer) Set(
	_ context.Context, request *azdext.SetUserConfigRequest,
) (*azdext.EmptyResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.requests = append(s.requests, "set:"+request.Path)
	if s.err != nil {
		return nil, s.err
	}
	name, ok := strings.CutPrefix(request.Path, config.UserConfigPath+".")
	if !ok {
		return nil, status.Error(codes.InvalidArgument, "unexpected configuration namespace")
	}
	if s.settings == nil {
		s.settings = make(map[string]json.RawMessage)
	}
	s.settings[name] = request.Value
	return &azdext.EmptyResponse{}, nil
}

func (s *userConfigServer) Unset(
	_ context.Context, request *azdext.UnsetUserConfigRequest,
) (*azdext.EmptyResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.requests = append(s.requests, "unset:"+request.Path)
	if s.err != nil {
		return nil, s.err
	}
	name, ok := strings.CutPrefix(request.Path, config.UserConfigPath+".")
	if !ok {
		return nil, status.Error(codes.InvalidArgument, "unexpected configuration namespace")
	}
	delete(s.settings, name)
	return &azdext.EmptyResponse{}, nil
}

func startUserConfigHost(t *testing.T, user *userConfigServer) {
	t.Helper()
	for key := range config.ProcessValues() {
		t.Setenv(key, "")
		require.NoError(t, os.Unsetenv(key))
	}
	t.Setenv("AZD_ACCESS_TOKEN", "private-host-token")
	// No project is available; auth, deployment, and service RPCs are not registered.
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	server := grpc.NewServer()
	azdext.RegisterUserConfigServiceServer(server, user)
	azdext.RegisterEnvironmentServiceServer(server, &projectlessConfigEnvironment{})
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	t.Cleanup(func() {
		server.Stop()
		if err := <-done; err != nil {
			assert.ErrorIs(t, err, grpc.ErrServerStopped)
		}
	})
	t.Setenv("AZD_SERVER", listener.Addr().String())
	t.Chdir(t.TempDir())
}

func TestCUConfigUsesAzdUserStoreWithoutProjectOrAuthentication(t *testing.T) {
	user := &userConfigServer{}
	startUserConfigHost(t, user)
	for _, test := range []struct {
		args []string
		want string
	}{
		{[]string{"config", "list"}, `{}`},
		{[]string{"config", "set", "CU_ENDPOINT", "https://resource.services.ai.azure.com"},
			`{"CU_ENDPOINT":"https://resource.services.ai.azure.com"}`},
		{[]string{"config", "get", "CU_ENDPOINT"}, `{"CU_ENDPOINT":"https://resource.services.ai.azure.com"}`},
		{[]string{"config", "set", "CU_DEFAULT_ANALYZER", "prebuilt-invoice"}, `{"CU_DEFAULT_ANALYZER":"prebuilt-invoice"}`},
		{[]string{"config", "set", "CU_API_VERSION", "2027-01-01-preview"}, `{"CU_API_VERSION":"2027-01-01-preview"}`},
	} {
		stdout, stderr, err := executeRoot(t, test.args...)
		require.NoError(t, err)
		assert.Empty(t, stderr)
		assert.JSONEq(t, test.want, stdout)
	}
	// A direct azd config write is visible without a separate CU store.
	client, err := azdext.NewAzdClient()
	require.NoError(t, err)
	defer client.Close()
	ctx := azdext.WithAccessToken(t.Context())
	_, err = client.UserConfig().Set(ctx, &azdext.SetUserConfigRequest{
		Path: config.UserConfigPath + ".CU_ENDPOINT", Value: []byte(`"https://direct.services.ai.azure.com"`),
	})
	require.NoError(t, err)
	t.Setenv("CU_ENDPOINT", "https://process.services.ai.azure.com")
	t.Setenv("AZD_ENVIRONMENT", "missing")
	stdout, stderr, err := executeRoot(t, "config", "get", "CU_ENDPOINT", "--output", "json")
	require.NoError(t, err)
	assert.Empty(t, stderr)
	assert.JSONEq(t, `{"CU_ENDPOINT":"https://direct.services.ai.azure.com"}`, stdout)
	stdout, _, err = executeRoot(t, "config", "list", "--output", "none")
	require.NoError(t, err)
	assert.Empty(t, stdout)
	_, _, err = executeRoot(t, "config", "unset", "CU_ENDPOINT")
	require.NoError(t, err)
	_, _, err = executeRoot(t, "config", "get", "CU_ENDPOINT")
	require.ErrorContains(t, err, "not set")
}

func TestCUConfigValidatesBeforeHostAccess(t *testing.T) {
	t.Setenv("AZD_SERVER", "")
	t.Setenv("AZD_ACCESS_TOKEN", "")
	for _, args := range [][]string{
		{"config", "set", "endpoint", "https://resource.services.ai.azure.com"},
		{"config", "set", "CU_API_KEY", "private-key"},
		{"config", "set", "CU_ENDPOINT",
			"https://user:private-password@resource.services.ai.azure.com?sig=private-signature#private-fragment"},
		{"config", "set", "CU_ENDPOINT", ""},
		{"config", "set", "CU_DEFAULT_ANALYZER", "private/analyzer"},
		{"config", "set", "CU_API_VERSION", "2027-02-29-preview"},
		{"config", "get", "cu_endpoint"},
		{"config", "unset", "apiVersion"},
		{"config", "list", "--environment", ""},
		{"config", "set", "CU_ENDPOINT", "https://resource.services.ai.azure.com", "--environment", "dev"},
	} {
		stdout, stderr, err := executeRoot(t, args...)
		require.Error(t, err)
		assert.NotContains(t, err.Error(), "host is unavailable")
		assert.NotContains(t, err.Error(), "private")
		assert.Empty(t, stdout)
		assert.Empty(t, stderr)
	}
}

func TestCUConfigRejectsUntrustedEndpointWithoutChangingStoredDefaults(t *testing.T) {
	const endpoint = `"https://resource.services.ai.azure.com"`
	user := &userConfigServer{settings: map[string]json.RawMessage{
		"CU_ENDPOINT": json.RawMessage(endpoint),
	}}
	startUserConfigHost(t, user)
	stdout, stderr, err := executeRoot(t, "config", "set", "CU_ENDPOINT", "https://private-attacker.example")
	require.ErrorContains(t, err, "Azure resource endpoint")
	assert.NotContains(t, err.Error(), "private")
	assert.Empty(t, stdout)
	assert.Empty(t, stderr)
	user.mu.Lock()
	defer user.mu.Unlock()
	assert.Empty(t, user.requests, "invalid configuration must not access or mutate the host store")
	assert.JSONEq(t, endpoint, string(user.settings["CU_ENDPOINT"]))
}

func TestCUConfigInvalidStoredValuesAndHostFailures(t *testing.T) {
	user := &userConfigServer{settings: map[string]json.RawMessage{
		"CU_ENDPOINT": json.RawMessage(
			`"https://user:private-password@resource.services.ai.azure.com?sig=private-signature"`),
	}}
	startUserConfigHost(t, user)
	for _, args := range [][]string{{"config", "get", "CU_ENDPOINT"}, {"config", "list"}} {
		stdout, stderr, err := executeRoot(t, args...)
		require.Error(t, err)
		assert.NotContains(t, err.Error(), "private")
		assert.Empty(t, stdout)
		assert.Empty(t, stderr)
	}
	// Invalid values remain removable.
	_, _, err := executeRoot(t, "config", "unset", "CU_ENDPOINT")
	require.NoError(t, err)
	user.mu.Lock()
	user.err = status.Error(codes.PermissionDenied, "private-host-detail")
	user.mu.Unlock()
	for _, args := range [][]string{
		{"config", "list"}, {"config", "set", "CU_API_VERSION", "2025-11-01"}, {"config", "unset", "CU_API_VERSION"},
	} {
		stdout, stderr, err := executeRoot(t, args...)
		require.Error(t, err)
		assert.NotContains(t, err.Error(), "private")
		assert.Empty(t, stdout)
		assert.Empty(t, stderr)
	}
}

func TestEmptyServiceFlagsFailBeforeHostAccess(t *testing.T) {
	t.Setenv("AZD_SERVER", "")
	for _, args := range [][]string{
		{"doctor", "--endpoint="}, {"analyzer", "list", "--api-version="},
		{"defaults", "show", "--endpoint="},
		{"doctor", "--environment="},
	} {
		_, _, err := executeRoot(t, args...)
		require.Error(t, err)
		assert.NotContains(t, err.Error(), "host is unavailable")
		assert.Contains(t, err.Error(), "must not be empty")
	}
}

type projectlessConfigEnvironment struct {
	azdext.UnimplementedEnvironmentServiceServer
}

func (*projectlessConfigEnvironment) GetCurrent(
	context.Context, *azdext.EmptyRequest,
) (*azdext.EnvironmentResponse, error) {
	return nil, status.Error(codes.Unknown, "no project exists; to create a new project, run `azd init`")
}

func TestDoctorUsesUserDefaultsThroughSharedRuntimeWithoutProject(t *testing.T) {
	user := &userConfigServer{settings: map[string]json.RawMessage{
		"CU_ENDPOINT":         json.RawMessage(`"https://user.services.ai.azure.com"`),
		"CU_API_VERSION":      json.RawMessage(`"2027-01-01-preview"`),
		"CU_DEFAULT_ANALYZER": json.RawMessage(`"prebuilt-invoice"`),
	}}
	startUserConfigHost(t, user)
	root := newRootCommand(func(
		cmd *cobra.Command, extCtx *azdext.ExtensionContext, options config.Options,
	) (*serviceSession, error) {
		session, err := openServiceSession(cmd, extCtx, options)
		if err != nil {
			return nil, err
		}
		session.client = &defaultsFake{current: cuclient.Defaults{ModelDeployments: map[string]string{}}}
		return session, nil
	})
	var stdout, stderr bytes.Buffer
	root.SetOut(&stdout)
	root.SetErr(&stderr)
	root.SetArgs([]string{"doctor", "--output", "json"})
	require.NoError(t, root.ExecuteContext(t.Context()))
	assert.Empty(t, stderr.String())
	var result doctorResult
	require.NoError(t, json.Unmarshal(stdout.Bytes(), &result))
	assert.Equal(t, "https://user.services.ai.azure.com/contentunderstanding", result.Endpoint)
	assert.Equal(t, "2027-01-01-preview", result.APIVersion)
	require.NotNil(t, result.Analyzer)
	assert.Equal(t, "prebuilt-invoice", *result.Analyzer)
	assert.Equal(t, "projectless", result.Profile)
	assert.Equal(t, map[string]string{
		"CU_ENDPOINT": "user config", "CU_API_VERSION": "user config", "CU_DEFAULT_ANALYZER": "user config",
	}, result.Sources)
}
