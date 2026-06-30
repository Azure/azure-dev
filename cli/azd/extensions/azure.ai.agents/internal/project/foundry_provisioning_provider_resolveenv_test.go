// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"context"
	"net"
	"testing"

	"azureaiagent/internal/exterrors"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// resolveEnvStubEnvServer is an EnvironmentService stub for resolveEnv tests. It
// serves a fixed env name, returns values from a keyed map (absent keys read as
// empty, which is what triggers the prompt path), and records SetValue writes.
type resolveEnvStubEnvServer struct {
	azdext.UnimplementedEnvironmentServiceServer
	envName string
	get     map[string]string
	getErr  map[string]error
	set     map[string]string
}

func (s *resolveEnvStubEnvServer) GetCurrent(
	context.Context, *azdext.EmptyRequest,
) (*azdext.EnvironmentResponse, error) {
	return &azdext.EnvironmentResponse{Environment: &azdext.Environment{Name: s.envName}}, nil
}

func (s *resolveEnvStubEnvServer) GetValue(
	_ context.Context, req *azdext.GetEnvRequest,
) (*azdext.KeyValueResponse, error) {
	if err := s.getErr[req.Key]; err != nil {
		return nil, err
	}
	return &azdext.KeyValueResponse{Value: s.get[req.Key]}, nil
}

func (s *resolveEnvStubEnvServer) SetValue(
	_ context.Context, req *azdext.SetEnvRequest,
) (*azdext.EmptyResponse, error) {
	if s.set == nil {
		s.set = map[string]string{}
	}
	s.set[req.Key] = req.Value
	return &azdext.EmptyResponse{}, nil
}

// resolveEnvStubPromptServer is a PromptService stub for resolveEnv tests. Each
// prompt returns its configured value, or its configured error when set.
type resolveEnvStubPromptServer struct {
	azdext.UnimplementedPromptServiceServer
	subscriptionID  string
	subscriptionErr error
	subscriptionN   int
	location        string
	locationErr     error
	locationN       int
}

func (s *resolveEnvStubPromptServer) PromptSubscription(
	context.Context, *azdext.PromptSubscriptionRequest,
) (*azdext.PromptSubscriptionResponse, error) {
	s.subscriptionN++
	if s.subscriptionErr != nil {
		return nil, s.subscriptionErr
	}
	return &azdext.PromptSubscriptionResponse{
		Subscription: &azdext.Subscription{Id: s.subscriptionID},
	}, nil
}

func (s *resolveEnvStubPromptServer) PromptLocation(
	context.Context, *azdext.PromptLocationRequest,
) (*azdext.PromptLocationResponse, error) {
	s.locationN++
	if s.locationErr != nil {
		return nil, s.locationErr
	}
	return &azdext.PromptLocationResponse{Location: &azdext.Location{Name: s.location}}, nil
}

// newResolveEnvTestClient spins up a gRPC server exposing the given environment
// and prompt stubs and returns an AzdClient connected to it.
func newResolveEnvTestClient(
	t *testing.T,
	envSrv azdext.EnvironmentServiceServer,
	promptSrv azdext.PromptServiceServer,
) *azdext.AzdClient {
	t.Helper()

	srv := grpc.NewServer()
	azdext.RegisterEnvironmentServiceServer(srv, envSrv)
	azdext.RegisterPromptServiceServer(srv, promptSrv)

	lis, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(func() {
		srv.Stop()
		_ = lis.Close()
	})

	client, err := azdext.NewAzdClient(azdext.WithAddress(lis.Addr().String()))
	require.NoError(t, err)
	t.Cleanup(func() { client.Close() })

	return client
}

func TestResolveEnv_PromptsAndPersistsSubscriptionAndLocation(t *testing.T) {
	// Neither AZURE_SUBSCRIPTION_ID nor AZURE_LOCATION is set: resolveEnv must
	// prompt for both (matching core `azd up`) and persist the choices, instead
	// of failing the way it used to (#8859).
	env := &resolveEnvStubEnvServer{envName: "foundry-bugbash", get: map[string]string{}}
	prompt := &resolveEnvStubPromptServer{
		subscriptionID: "00000000-0000-0000-0000-000000000001",
		location:       "westus2",
	}
	client := newResolveEnvTestClient(t, env, prompt)

	p := &FoundryProvisioningProvider{azdClient: client}
	require.NoError(t, p.resolveEnv(t.Context()))

	assert.Equal(t, 1, prompt.subscriptionN, "expected a subscription prompt")
	assert.Equal(t, 1, prompt.locationN, "expected a location prompt")
	assert.Equal(t, "00000000-0000-0000-0000-000000000001", p.subID)
	assert.Equal(t, "westus2", p.location)
	assert.Equal(t, "00000000-0000-0000-0000-000000000001", env.set[envKeySubscriptionID],
		"subscription should be persisted to the azd environment")
	assert.Equal(t, "westus2", env.set[envKeyLocation],
		"location should be persisted to the azd environment")
}

func TestResolveEnv_NoPromptSubscriptionReturnsActionableError(t *testing.T) {
	// Under `--no-prompt` the azd host returns a "prompt required" error. The
	// provider must surface an actionable suggestion naming the env var so CI
	// and scripts stay deterministic.
	env := &resolveEnvStubEnvServer{envName: "foundry-bugbash", get: map[string]string{}}
	prompt := &resolveEnvStubPromptServer{
		subscriptionErr: status.Error(codes.FailedPrecondition, "prompt required: no terminal"),
	}
	client := newResolveEnvTestClient(t, env, prompt)

	p := &FoundryProvisioningProvider{azdClient: client}
	err := p.resolveEnv(t.Context())
	require.Error(t, err)

	var local *azdext.LocalError
	require.ErrorAs(t, err, &local)
	assert.Equal(t, exterrors.CodeMissingAzureSubscription, local.Code)
	assert.Equal(t, azdext.LocalErrorCategoryDependency, local.Category)
	assert.Contains(t, local.Suggestion, envKeySubscriptionID)
	assert.Empty(t, env.set, "nothing should be persisted when the prompt fails")
}

func TestResolveEnv_NoPromptLocationReturnsActionableError(t *testing.T) {
	// Subscription is already set, but AZURE_LOCATION is not. Under `--no-prompt`
	// the location prompt fails and must yield an actionable AZURE_LOCATION error.
	env := &resolveEnvStubEnvServer{
		envName: "foundry-bugbash",
		get:     map[string]string{envKeySubscriptionID: "00000000-0000-0000-0000-000000000001"},
	}
	prompt := &resolveEnvStubPromptServer{
		locationErr: status.Error(codes.FailedPrecondition, "prompt required: no terminal"),
	}
	client := newResolveEnvTestClient(t, env, prompt)

	p := &FoundryProvisioningProvider{azdClient: client}
	err := p.resolveEnv(t.Context())
	require.Error(t, err)

	assert.Equal(t, 0, prompt.subscriptionN, "subscription was already set; no prompt expected")
	var local *azdext.LocalError
	require.ErrorAs(t, err, &local)
	assert.Equal(t, exterrors.CodeMissingAzureLocation, local.Code)
	assert.Equal(t, azdext.LocalErrorCategoryDependency, local.Category)
	assert.Contains(t, local.Suggestion, envKeyLocation)
}

func TestResolveEnv_CancelledSubscriptionPromptReturnsCancelled(t *testing.T) {
	// A user-cancelled subscription prompt must map to the cancellation category,
	// not a missing-dependency error.
	env := &resolveEnvStubEnvServer{envName: "foundry-bugbash", get: map[string]string{}}
	prompt := &resolveEnvStubPromptServer{
		subscriptionErr: status.Error(codes.Canceled, "user cancelled"),
	}
	client := newResolveEnvTestClient(t, env, prompt)

	p := &FoundryProvisioningProvider{azdClient: client}
	err := p.resolveEnv(t.Context())
	require.Error(t, err)

	var local *azdext.LocalError
	require.ErrorAs(t, err, &local)
	assert.Equal(t, exterrors.CodeCancelled, local.Code)
}

func TestResolveEnv_CancelledLocationPromptReturnsCancelled(t *testing.T) {
	// Subscription resolves, but the user cancels the location prompt. resolveEnv
	// must return a cancellation error and not persist partial state.
	env := &resolveEnvStubEnvServer{
		envName: "foundry-bugbash",
		get:     map[string]string{envKeySubscriptionID: "00000000-0000-0000-0000-000000000001"},
	}
	prompt := &resolveEnvStubPromptServer{
		locationErr: status.Error(codes.Canceled, "user cancelled"),
	}
	client := newResolveEnvTestClient(t, env, prompt)

	p := &FoundryProvisioningProvider{azdClient: client}
	err := p.resolveEnv(t.Context())
	require.Error(t, err)

	var local *azdext.LocalError
	require.ErrorAs(t, err, &local)
	assert.Equal(t, exterrors.CodeCancelled, local.Code)
}

func TestResolveEnv_SubscriptionReadErrorSurfaces(t *testing.T) {
	// A genuine environment read failure must be surfaced, not masked by a
	// prompt: GetValue returns ("", nil) for an unset key, so an error here
	// means the environment itself could not be read.
	env := &resolveEnvStubEnvServer{
		envName: "foundry-bugbash",
		get:     map[string]string{},
		getErr:  map[string]error{envKeySubscriptionID: status.Error(codes.Internal, "env read failed")},
	}
	prompt := &resolveEnvStubPromptServer{}
	client := newResolveEnvTestClient(t, env, prompt)

	p := &FoundryProvisioningProvider{azdClient: client}
	err := p.resolveEnv(t.Context())
	require.Error(t, err)

	assert.Equal(t, 0, prompt.subscriptionN, "a read failure must not trigger a prompt")
	var local *azdext.LocalError
	require.ErrorAs(t, err, &local)
	assert.Equal(t, exterrors.CodeEnvironmentValuesFailed, local.Code)
}

func TestResolveEnv_EmptySubscriptionResponseReturnsError(t *testing.T) {
	// Defensive: a subscription response with a blank id must not be persisted;
	// fail with an actionable error instead of writing an empty value.
	env := &resolveEnvStubEnvServer{envName: "foundry-bugbash", get: map[string]string{}}
	prompt := &resolveEnvStubPromptServer{subscriptionID: "   "}
	client := newResolveEnvTestClient(t, env, prompt)

	p := &FoundryProvisioningProvider{azdClient: client}
	err := p.resolveEnv(t.Context())
	require.Error(t, err)

	var local *azdext.LocalError
	require.ErrorAs(t, err, &local)
	assert.Equal(t, exterrors.CodeMissingAzureSubscription, local.Code)
	assert.Empty(t, env.set, "an empty subscription id must not be persisted")
}

func TestResolveEnv_LocationReadErrorSurfaces(t *testing.T) {
	// A location read failure is distinct from an unset AZURE_LOCATION value and
	// must be surfaced instead of falling through to the location prompt.
	env := &resolveEnvStubEnvServer{
		envName: "foundry-bugbash",
		get:     map[string]string{envKeySubscriptionID: "00000000-0000-0000-0000-000000000001"},
		getErr:  map[string]error{envKeyLocation: status.Error(codes.Internal, "env read failed")},
	}
	prompt := &resolveEnvStubPromptServer{}
	client := newResolveEnvTestClient(t, env, prompt)

	p := &FoundryProvisioningProvider{azdClient: client}
	err := p.resolveEnv(t.Context())
	require.Error(t, err)

	assert.Equal(t, 0, prompt.subscriptionN, "subscription was already set; no prompt expected")
	assert.Equal(t, 0, prompt.locationN, "a read failure must not trigger a prompt")
	var local *azdext.LocalError
	require.ErrorAs(t, err, &local)
	assert.Equal(t, exterrors.CodeEnvironmentValuesFailed, local.Code)
}

func TestResolveEnv_EmptyLocationResponseReturnsError(t *testing.T) {
	// Defensive: a location response with a blank name must not be persisted;
	// fail with an actionable error instead of writing an empty value.
	env := &resolveEnvStubEnvServer{
		envName: "foundry-bugbash",
		get:     map[string]string{envKeySubscriptionID: "00000000-0000-0000-0000-000000000001"},
	}
	prompt := &resolveEnvStubPromptServer{location: "   "}
	client := newResolveEnvTestClient(t, env, prompt)

	p := &FoundryProvisioningProvider{azdClient: client}
	err := p.resolveEnv(t.Context())
	require.Error(t, err)

	var local *azdext.LocalError
	require.ErrorAs(t, err, &local)
	assert.Equal(t, exterrors.CodeMissingAzureLocation, local.Code)
	assert.Empty(t, env.set, "an empty location name must not be persisted")
}
