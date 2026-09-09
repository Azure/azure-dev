// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"testing"

	"azure.ai.routines/internal/exterrors"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/types/known/structpb"
)

func TestParseRoutineServiceConfig_ServiceLevel(t *testing.T) {
	t.Parallel()

	props, err := structpb.NewStruct(map[string]any{
		"description": "nightly summary",
		"enabled":     true,
		"triggers": map[string]any{
			"default": map[string]any{"type": "recurring", "cron_expression": "0 9 * * *"},
		},
		"action": map[string]any{"type": "invoke_agent_responses_api", "agent_name": "summarizer"},
	})
	require.NoError(t, err)

	body, err := parseRoutineServiceConfig(&azdext.ServiceConfig{
		Name:                 "nightly",
		Host:                 aiRoutineHost,
		AdditionalProperties: props,
	}, "")
	require.NoError(t, err)
	assert.Equal(t, "nightly summary", body.Description)
	require.NotNil(t, body.Enabled)
	assert.True(t, *body.Enabled)
	require.Contains(t, body.Triggers, "default")
	assert.Equal(t, "recurring", body.Triggers["default"].Type)
	assert.Equal(t, "0 9 * * *", body.Triggers["default"].CronExpression)
	require.NotNil(t, body.Action)
	assert.Equal(t, "summarizer", body.Action.AgentName)
}

// TestParseRoutineServiceConfig_ConfigFallback verifies routines written before
// the per-resource service split (config-nested shape) still parse.
func TestParseRoutineServiceConfig_ConfigFallback(t *testing.T) {
	t.Parallel()

	props, err := structpb.NewStruct(map[string]any{"description": "legacy"})
	require.NoError(t, err)

	body, err := parseRoutineServiceConfig(&azdext.ServiceConfig{
		Name:   "legacy",
		Host:   aiRoutineHost,
		Config: props,
	}, "")
	require.NoError(t, err)
	assert.Equal(t, "legacy", body.Description)
}

func TestParseRoutineServiceConfig_MixedShapeUsesInlineProperties(t *testing.T) {
	t.Parallel()

	body, err := parseRoutineServiceConfig(&azdext.ServiceConfig{
		Name:                 "mixed",
		Host:                 aiRoutineHost,
		AdditionalProperties: mustStruct(t, map[string]any{"custom": "inline"}),
		Config:               mustStruct(t, map[string]any{"description": "legacy"}),
	}, "")
	require.NoError(t, err)
	assert.Empty(t, body.Description)
}

func TestParseRoutineServiceConfig_FileRef(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	require.NoError(t, os.WriteFile(
		filepath.Join(root, "routine.yaml"),
		[]byte("description: referenced routine\n"+
			"triggers:\n"+
			"  default:\n"+
			"    type: schedule\n"+
			"    cron_expression: \"0 2 * * *\"\n"+
			"action:\n"+
			"  type: invoke_agent_responses_api\n"+
			"  agent_name: summarizer\n"+
			"  input:\n"+
			"    $ref: literal-payload-reference\n"+
			"    project: literal-project-value\n"+
			"    instructions: literal-instructions.md\n"),
		0o600,
	))
	props, err := structpb.NewStruct(map[string]any{"$ref": "./routine.yaml"})
	require.NoError(t, err)

	body, err := parseRoutineServiceConfig(&azdext.ServiceConfig{
		Name:                 "nightly",
		Host:                 aiRoutineHost,
		AdditionalProperties: props,
	}, root)
	require.NoError(t, err)
	assert.Equal(t, "referenced routine", body.Description)
	assert.Equal(t, "0 2 * * *", body.Triggers["default"].CronExpression)
	require.NotNil(t, body.Action)
	assert.Equal(t, "summarizer", body.Action.AgentName)
	assert.Equal(t, map[string]any{
		"$ref":         "literal-payload-reference",
		"project":      "literal-project-value",
		"instructions": "literal-instructions.md",
	}, body.Action.Input)
}

func TestParseRoutineServiceConfig_FileRefOverlay(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	require.NoError(t, os.WriteFile(
		filepath.Join(root, "routine.yaml"),
		[]byte("description: referenced routine\nenabled: true\n"),
		0o600,
	))
	props, err := structpb.NewStruct(map[string]any{
		"$ref":        "./routine.yaml",
		"description": "inline override",
	})
	require.NoError(t, err)

	body, err := parseRoutineServiceConfig(&azdext.ServiceConfig{
		Name:                 "nightly",
		Host:                 aiRoutineHost,
		AdditionalProperties: props,
	}, root)
	require.NoError(t, err)
	assert.Equal(t, "inline override", body.Description)
	require.NotNil(t, body.Enabled)
	assert.True(t, *body.Enabled)
}

func TestResolveRoutineServiceRef_AbsolutePath(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "routine.yaml")
	require.NoError(t, os.WriteFile(path, []byte("description: absolute routine\n"), 0o600))

	resolved, err := resolveRoutineServiceRef(map[string]any{"$ref": path}, "")
	require.NoError(t, err)
	assert.Equal(t, "absolute routine", resolved["description"])
}

func TestResolveRoutineServiceRef_LocalPathWithPercent(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	require.NoError(t, os.WriteFile(
		filepath.Join(root, "100%-ready.yaml"),
		[]byte("description: percent routine\n"),
		0o600,
	))

	resolved, err := resolveRoutineServiceRef(map[string]any{"$ref": "./100%-ready.yaml"}, root)
	require.NoError(t, err)
	assert.Equal(t, "percent routine", resolved["description"])
}

func TestRemoteRoutineRefPattern(t *testing.T) {
	t.Parallel()

	assert.True(t, remoteRoutineRefPattern.MatchString("https://example.com/routine.yaml"))
	assert.True(t, remoteRoutineRefPattern.MatchString("git+https://example.com/routine.yaml"))
	assert.False(t, remoteRoutineRefPattern.MatchString("routine:v1.yaml"))
	assert.False(t, remoteRoutineRefPattern.MatchString("./routine.yaml"))
}

func TestResolveRoutineServiceRef_ValidationErrors(t *testing.T) {
	t.Parallel()

	markers := []string{"marker-user", "marker-password", "marker-signature"}
	remoteRef := fmt.Sprintf(
		"https://%s:%s@example.com/routine.yaml?sig=%s",
		markers[0], markers[1], markers[2],
	)
	tests := []struct {
		name        string
		values      map[string]any
		projectRoot string
		message     string
		notContains []string
	}{
		{
			name: "non-string", values: map[string]any{"$ref": 42},
			projectRoot: t.TempDir(), message: "non-empty string",
		},
		{
			name: "empty", values: map[string]any{"$ref": "  "},
			projectRoot: t.TempDir(), message: "non-empty string",
		},
		{
			name:        "remote",
			values:      map[string]any{"$ref": remoteRef},
			projectRoot: t.TempDir(), message: "not supported",
			notContains: append(markers, "example.com"),
		},
		{
			name: "malformed remote", values: map[string]any{"$ref": "https://example.com/%zz"},
			projectRoot: t.TempDir(), message: "not supported",
		},
		{
			name: "missing project path", values: map[string]any{"$ref": "./routine.yaml"},
			message: "without an azure.yaml project path",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			_, err := resolveRoutineServiceRef(test.values, test.projectRoot)
			localErr, ok := errors.AsType[*azdext.LocalError](err)
			require.True(t, ok)
			assert.Equal(t, exterrors.CodeInvalidRoutineManifest, localErr.Code)
			assert.Equal(t, azdext.LocalErrorCategoryValidation, localErr.Category)
			assert.Contains(t, localErr.Message, test.message)
			for _, secret := range test.notContains {
				assert.NotContains(t, localErr.Message, secret)
			}
			assert.NotEmpty(t, localErr.Suggestion)
		})
	}
}

func TestExpandRoutineValue(t *testing.T) {
	t.Parallel()

	serviceConfig := &azdext.ServiceConfig{
		Environment: map[string]string{"DIGEST_TOPIC": "weekly changes"},
	}
	environment, err := (&routineServiceTarget{}).environmentValues(
		t.Context(),
		serviceConfig,
	)
	require.NoError(t, err)
	input := map[string]any{
		"topic":  "${DIGEST_TOPIC}",
		"secret": "${{connections.search.credentials.key}}",
	}

	assert.Equal(t, map[string]any{
		"topic":  "weekly changes",
		"secret": "${{connections.search.credentials.key}}",
	}, expandRoutineValue(input, environment))
}

// fakeServiceConfigReader reports a fixed env-declared result.
type fakeServiceConfigReader struct {
	found       bool
	projectPath string
	getErr      error
}

func (f fakeServiceConfigReader) Get(
	context.Context,
	*azdext.EmptyRequest,
	...grpc.CallOption,
) (*azdext.GetProjectResponse, error) {
	return &azdext.GetProjectResponse{
		Project: &azdext.ProjectConfig{Path: f.projectPath},
	}, f.getErr
}

func (f fakeServiceConfigReader) GetServiceConfigValue(
	context.Context,
	*azdext.GetServiceConfigValueRequest,
	...grpc.CallOption,
) (*azdext.GetServiceConfigValueResponse, error) {
	return &azdext.GetServiceConfigValueResponse{Found: f.found}, nil
}

func TestRoutineProjectRoot(t *testing.T) {
	t.Parallel()

	refProperties, err := structpb.NewStruct(map[string]any{"$ref": "./routine.yaml"})
	require.NoError(t, err)
	root, err := routineProjectRoot(
		t.Context(),
		fakeServiceConfigReader{projectPath: "/project"},
		&azdext.ServiceConfig{Name: "nightly", AdditionalProperties: refProperties},
	)
	require.NoError(t, err)
	assert.Equal(t, "/project", root)

	root, err = routineProjectRoot(
		t.Context(),
		fakeServiceConfigReader{getErr: assert.AnError},
		&azdext.ServiceConfig{Name: "inline"},
	)
	require.NoError(t, err)
	assert.Empty(t, root)

	_, err = routineProjectRoot(
		t.Context(),
		fakeServiceConfigReader{getErr: assert.AnError},
		&azdext.ServiceConfig{Name: "nightly", AdditionalProperties: refProperties},
	)
	require.ErrorIs(t, err, assert.AnError)
}

func TestRoutineEnvironmentValuesEmptyDeclaredIsolates(t *testing.T) {
	t.Parallel()

	target := &routineServiceTarget{
		projectClient: fakeServiceConfigReader{found: true},
	}
	env, err := target.environmentValues(
		t.Context(),
		&azdext.ServiceConfig{Name: "nightly-digest"},
	)
	require.NoError(t, err)
	require.Empty(t, env)
}

// TestRoutineServiceTargetDeployPropagatesAccessToken covers gRPC auth.
func TestRoutineServiceTargetDeployPropagatesAccessToken(t *testing.T) {
	const accessToken = "test-extension-token"

	t.Setenv("AZD_ACCESS_TOKEN", accessToken)
	stubAzdProjectSources(t, azdProjectSources{
		EnvValue: "https://test.services.ai.azure.com/api/projects/test",
	}, nil)

	server := &routineAuthMetadataServer{
		environmentAuth: make(chan string, 2),
		accountAuth:     make(chan string, 1),
	}
	azdClient := newRoutineAuthAzdClient(t, server)
	target := &routineServiceTarget{
		azdClient:     azdClient,
		projectClient: fakeServiceConfigReader{},
	}

	_, err := target.Deploy(
		t.Context(),
		&azdext.ServiceConfig{
			Name:        "nightly",
			Host:        aiRoutineHost,
			Environment: map[string]string{"TEST_VALUE": "value"},
		},
		nil,
		nil,
		nil,
	)

	require.ErrorContains(t, err, "resolving user access tenant")
	assert.Equal(t, accessToken, <-server.environmentAuth)
	assert.Equal(t, accessToken, <-server.environmentAuth)
	assert.Equal(t, accessToken, <-server.accountAuth)
}

func TestResolveRoutineServiceTenant(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name            string
		environment     *stubRoutineEnvironment
		account         *stubRoutineAccount
		wantTenant      string
		wantErr         string
		wantEnvironment string
		wantSubID       string
	}{
		{
			name: "uses user access tenant for active environment subscription",
			environment: &stubRoutineEnvironment{
				name:           "dev",
				subscriptionID: "subscription-id",
			},
			account:         &stubRoutineAccount{tenantID: "user-access-tenant"},
			wantTenant:      "user-access-tenant",
			wantEnvironment: "dev",
			wantSubID:       "subscription-id",
		},
		{
			name:        "fails when subscription is missing",
			environment: &stubRoutineEnvironment{name: "dev"},
			account:     &stubRoutineAccount{},
			wantErr:     "AZURE_SUBSCRIPTION_ID is required",
		},
		{
			name: "propagates tenant lookup failure",
			environment: &stubRoutineEnvironment{
				name:           "dev",
				subscriptionID: "subscription-id",
			},
			account: &stubRoutineAccount{err: errors.New("lookup failed")},
			wantErr: "resolving user access tenant: lookup failed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			tenantID, err := resolveRoutineServiceTenant(
				t.Context(),
				tt.environment,
				tt.account,
			)

			if tt.wantErr != "" {
				require.ErrorContains(t, err, tt.wantErr)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.wantTenant, tenantID)
			assert.Equal(t, tt.wantEnvironment, tt.environment.gotEnvironment)
			assert.Equal(t, tt.wantSubID, tt.account.gotSubscriptionID)
		})
	}
}

type stubRoutineEnvironment struct {
	name           string
	subscriptionID string
	currentErr     error
	valueErr       error
	gotEnvironment string
}

func (s *stubRoutineEnvironment) GetCurrent(
	context.Context,
	*azdext.EmptyRequest,
	...grpc.CallOption,
) (*azdext.EnvironmentResponse, error) {
	if s.currentErr != nil {
		return nil, s.currentErr
	}
	return &azdext.EnvironmentResponse{
		Environment: &azdext.Environment{Name: s.name},
	}, nil
}

func (s *stubRoutineEnvironment) GetValue(
	_ context.Context,
	request *azdext.GetEnvRequest,
	_ ...grpc.CallOption,
) (*azdext.KeyValueResponse, error) {
	s.gotEnvironment = request.GetEnvName()
	if s.valueErr != nil {
		return nil, s.valueErr
	}
	return &azdext.KeyValueResponse{Value: s.subscriptionID}, nil
}

type stubRoutineAccount struct {
	tenantID          string
	err               error
	gotSubscriptionID string
}

func (s *stubRoutineAccount) LookupTenant(
	_ context.Context,
	request *azdext.LookupTenantRequest,
	_ ...grpc.CallOption,
) (*azdext.LookupTenantResponse, error) {
	s.gotSubscriptionID = request.GetSubscriptionId()
	if s.err != nil {
		return nil, s.err
	}
	return &azdext.LookupTenantResponse{TenantId: s.tenantID}, nil
}

type routineAuthMetadataServer struct {
	azdext.UnimplementedAccountServiceServer
	azdext.UnimplementedEnvironmentServiceServer
	environmentAuth chan string
	accountAuth     chan string
}

func (s *routineAuthMetadataServer) GetCurrent(
	ctx context.Context,
	_ *azdext.EmptyRequest,
) (*azdext.EnvironmentResponse, error) {
	s.environmentAuth <- incomingAuthorization(ctx)
	return &azdext.EnvironmentResponse{
		Environment: &azdext.Environment{Name: "dev"},
	}, nil
}

func (s *routineAuthMetadataServer) GetValue(
	ctx context.Context,
	_ *azdext.GetEnvRequest,
) (*azdext.KeyValueResponse, error) {
	s.environmentAuth <- incomingAuthorization(ctx)
	return &azdext.KeyValueResponse{Value: "subscription-id"}, nil
}

func (s *routineAuthMetadataServer) LookupTenant(
	ctx context.Context,
	_ *azdext.LookupTenantRequest,
) (*azdext.LookupTenantResponse, error) {
	s.accountAuth <- incomingAuthorization(ctx)
	return nil, errors.New("stop after auth assertion")
}

func incomingAuthorization(ctx context.Context) string {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return ""
	}
	values := md.Get("authorization")
	if len(values) == 0 {
		return ""
	}
	return values[0]
}

func newRoutineAuthAzdClient(
	t *testing.T,
	server *routineAuthMetadataServer,
) *azdext.AzdClient {
	t.Helper()

	grpcServer := grpc.NewServer()
	azdext.RegisterAccountServiceServer(grpcServer, server)
	azdext.RegisterEnvironmentServiceServer(grpcServer, server)

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	go func() { _ = grpcServer.Serve(listener) }()

	t.Cleanup(func() {
		grpcServer.Stop()
		_ = listener.Close()
	})

	azdClient, err := azdext.NewAzdClient(
		azdext.WithAddress(listener.Addr().String()),
	)
	require.NoError(t, err)
	t.Cleanup(azdClient.Close)

	return azdClient
}
