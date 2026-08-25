// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package provisioning

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"testing"

	"azure.ai.projects/internal/exterrors"
	"azure.ai.projects/internal/synthesis"

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
	envName    string
	currentErr error
	get        map[string]string
	getByEnv   map[string]map[string]string
	getErr     map[string]error
	set        map[string]string
}

func (s *resolveEnvStubEnvServer) GetCurrent(
	context.Context, *azdext.EmptyRequest,
) (*azdext.EnvironmentResponse, error) {
	if s.currentErr != nil {
		return nil, s.currentErr
	}
	return &azdext.EnvironmentResponse{Environment: &azdext.Environment{Name: s.envName}}, nil
}

func (s *resolveEnvStubEnvServer) GetValue(
	_ context.Context, req *azdext.GetEnvRequest,
) (*azdext.KeyValueResponse, error) {
	if err := s.getErr[req.Key]; err != nil {
		return nil, err
	}
	if values, ok := s.getByEnv[req.EnvName]; ok {
		return &azdext.KeyValueResponse{Value: values[req.Key]}, nil
	}
	return &azdext.KeyValueResponse{Value: s.get[req.Key]}, nil
}

func (s *resolveEnvStubEnvServer) GetValues(
	_ context.Context, req *azdext.GetEnvironmentRequest,
) (*azdext.KeyValueListResponse, error) {
	values := s.get
	if envValues, ok := s.getByEnv[req.Name]; ok {
		values = envValues
	}
	keyValues := make([]*azdext.KeyValue, 0, len(values))
	for key, value := range values {
		keyValues = append(keyValues, &azdext.KeyValue{Key: key, Value: value})
	}
	return &azdext.KeyValueListResponse{KeyValues: keyValues}, nil
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

func TestResolveEnv_UsesCommandEnvironment(t *testing.T) {
	env := &resolveEnvStubEnvServer{
		currentErr: errors.New("no persisted environment is selected"),
		get: map[string]string{
			envKeySubscriptionID: "00000000-0000-0000-0000-000000000001",
			envKeyLocation:       "westus2",
			envKeyFoundryRG:      "rg-platform-foundry",
		},
	}
	client := newResolveEnvTestClient(t, env, &resolveEnvStubPromptServer{})
	p := &FoundryProvisioningProvider{
		azdClient:        client,
		requestedEnvName: " selected ",
	}

	require.NoError(t, p.resolveEnv(t.Context()))
	assert.Equal(t, "selected", p.envName)
}

func TestNetworkEnvMap_UsesResolvedEnvironment(t *testing.T) {
	env := &resolveEnvStubEnvServer{
		envName: "default",
		getByEnv: map[string]map[string]string{
			"default":  {"NETWORK_RESOURCE_ID": "from-default"},
			"selected": {"NETWORK_RESOURCE_ID": "from-selected"},
		},
	}
	client := newResolveEnvTestClient(t, env, &resolveEnvStubPromptServer{})
	p := &FoundryProvisioningProvider{
		azdClient: client,
		envName:   "selected",
	}

	assert.Equal(t, "from-selected", p.networkEnvMap(t.Context())["NETWORK_RESOURCE_ID"])
}

func TestResolveEnv_UsesVirtualEnvFromPreviousLayer(t *testing.T) {
	env := &resolveEnvStubEnvServer{envName: "foundry-layer", get: map[string]string{}}
	prompt := &resolveEnvStubPromptServer{}
	client := newResolveEnvTestClient(t, env, prompt)

	p := &FoundryProvisioningProvider{
		azdClient: client,
		isLayer:   true,
		virtualEnv: map[string]string{
			envKeySubscriptionID: "00000000-0000-0000-0000-000000000001",
			envKeyLocation:       "westus2",
			envKeyFoundryRG:      "rg-platform-foundry",
		},
	}
	require.NoError(t, p.resolveEnv(t.Context()))

	assert.Equal(t, "00000000-0000-0000-0000-000000000001", p.subID)
	assert.Equal(t, "westus2", p.location)
	assert.Equal(t, "rg-platform-foundry", p.rgName)
	assert.Zero(t, prompt.subscriptionN)
	assert.Zero(t, prompt.locationN)
}

func TestResolveEnv_LoadsLayerResourceGroupOwnership(t *testing.T) {
	const ownerID = "/subscriptions/00000000-0000-0000-0000-000000000001/resourceGroups/rg-platform-foundry"
	env := &resolveEnvStubEnvServer{envName: "foundry-layer", get: map[string]string{
		envKeySubscriptionID: "00000000-0000-0000-0000-000000000001",
		envKeyLocation:       "westus2",
		envKeyFoundryRG:      "rg-platform-foundry",
		envKeyFoundryRGOwner: ownerID,
	}}
	client := newResolveEnvTestClient(t, env, &resolveEnvStubPromptServer{})
	p := &FoundryProvisioningProvider{azdClient: client, isLayer: true}

	require.NoError(t, p.resolveEnv(t.Context()))
	assert.Equal(t, ownerID, p.foundryRGOwnerID)
}

func TestResolveEnv_LayerDefaultResourceGroupIsNotPersistedOrOwned(t *testing.T) {
	env := &resolveEnvStubEnvServer{envName: "foundry-layer", get: map[string]string{
		envKeySubscriptionID: "00000000-0000-0000-0000-000000000001",
		envKeyLocation:       "westus2",
	}}
	client := newResolveEnvTestClient(t, env, &resolveEnvStubPromptServer{})

	p := &FoundryProvisioningProvider{azdClient: client, isLayer: true}
	require.NoError(t, p.resolveEnv(t.Context()))

	assert.Equal(t, "rg-foundry-layer-foundry", p.rgName)
	assert.False(t, p.rgExplicit)
	assert.NotContains(t, env.set, envKeyFoundryRG)
}

func TestResolveEnvironmentNameErrorsAreActionable(t *testing.T) {
	resolvers := []struct {
		name string
		run  func(*FoundryProvisioningProvider, context.Context) error
	}{
		{
			name: "greenfield",
			run: func(provider *FoundryProvisioningProvider, ctx context.Context) error {
				return provider.resolveEnv(ctx)
			},
		},
		{
			name: "brownfield",
			run: func(provider *FoundryProvisioningProvider, ctx context.Context) error {
				return provider.resolveEnvName(ctx)
			},
		},
	}
	failures := []struct {
		name       string
		currentErr error
	}{
		{
			name:       "no current environment selected",
			currentErr: status.Error(codes.NotFound, "no current azd environment is selected"),
		},
		{
			name: "environment has no name",
		},
	}

	for _, resolver := range resolvers {
		t.Run(resolver.name, func(t *testing.T) {
			for _, failure := range failures {
				t.Run(failure.name, func(t *testing.T) {
					env := &resolveEnvStubEnvServer{currentErr: failure.currentErr}
					prompt := &resolveEnvStubPromptServer{}
					client := newResolveEnvTestClient(t, env, prompt)
					provider := &FoundryProvisioningProvider{azdClient: client}

					err := resolver.run(provider, t.Context())
					require.Error(t, err)
					assert.Equal(t, "azd environment name is required", err.Error())

					var local *azdext.LocalError
					require.ErrorAs(t, err, &local)
					assert.Equal(t, exterrors.CodeEnvironmentNotFound, local.Code)
					assert.Equal(t, azdext.LocalErrorCategoryDependency, local.Category)
					assert.Contains(t, local.Suggestion, "azd -e dev provision")
					assert.Contains(t, local.Suggestion, `$env:AZD_ENVIRONMENT = "dev"; azd provision`)
					assert.Contains(t, local.Suggestion, "azd env select dev")
					assert.NotEmpty(t, local.Suggestion)

					assert.Zero(t, prompt.subscriptionN)
					assert.Zero(t, prompt.locationN)
				})
			}
		})
	}
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
	assert.Contains(t, local.Suggestion,
		"azd env set "+envKeySubscriptionID+" 11111111-1111-1111-1111-111111111111")
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
	assert.Contains(t, local.Suggestion, "azd env set "+envKeyLocation+" eastus2")
}

func TestResolveEnv_PromptFailuresIncludeExactEnvSetCommands(t *testing.T) {
	tests := []struct {
		name        string
		envValues   map[string]string
		prompt      *resolveEnvStubPromptServer
		wantCode    string
		wantCommand string
	}{
		{
			name:      "subscription",
			envValues: map[string]string{},
			prompt: &resolveEnvStubPromptServer{
				subscriptionErr: status.Error(codes.Internal, "subscription prompt failed"),
			},
			wantCode: exterrors.CodeMissingAzureSubscription,
			wantCommand: "azd env set " + envKeySubscriptionID +
				" 11111111-1111-1111-1111-111111111111",
		},
		{
			name: "location",
			envValues: map[string]string{
				envKeySubscriptionID: "00000000-0000-0000-0000-000000000001",
			},
			prompt: &resolveEnvStubPromptServer{
				locationErr: status.Error(codes.Internal, "location prompt failed"),
			},
			wantCode:    exterrors.CodeMissingAzureLocation,
			wantCommand: "azd env set " + envKeyLocation + " eastus2",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			env := &resolveEnvStubEnvServer{
				envName: "foundry-bugbash",
				get:     test.envValues,
			}
			client := newResolveEnvTestClient(t, env, test.prompt)
			provider := &FoundryProvisioningProvider{azdClient: client}

			err := provider.resolveEnv(t.Context())
			require.Error(t, err)

			var local *azdext.LocalError
			require.ErrorAs(t, err, &local)
			assert.Equal(t, test.wantCode, local.Code)
			assert.Contains(t, local.Suggestion, test.wantCommand)
			assert.NotEmpty(t, local.Suggestion)
		})
	}
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

func TestResolveEnv_OptionalValueReadErrorsSurface(t *testing.T) {
	for _, key := range []string{envKeyFoundryRG, envKeyFoundryRGOwner, envKeyProjectName, envKeyPrincipalID} {
		t.Run(key, func(t *testing.T) {
			env := &resolveEnvStubEnvServer{
				envName: "foundry-layer",
				get: map[string]string{
					envKeySubscriptionID: "00000000-0000-0000-0000-000000000001",
					envKeyLocation:       "westus2",
				},
				getErr: map[string]error{key: status.Error(codes.Internal, "env read failed")},
			}
			client := newResolveEnvTestClient(t, env, &resolveEnvStubPromptServer{})
			p := &FoundryProvisioningProvider{azdClient: client, isLayer: true}

			err := p.resolveEnv(t.Context())
			require.Error(t, err)
			var local *azdext.LocalError
			require.ErrorAs(t, err, &local)
			assert.Equal(t, exterrors.CodeEnvironmentValuesFailed, local.Code)
		})
	}
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

// promptOrderStubProjectServer models azd core expanding ${VAR} in a
// service env at call time: the connection endpoint reads as empty
// until the prompted location has been persisted to the azd
// environment.
type promptOrderStubProjectServer struct {
	azdext.UnimplementedProjectServiceServer
	projectPath string
	env         *resolveEnvStubEnvServer
}

func (s *promptOrderStubProjectServer) Get(
	context.Context, *azdext.EmptyRequest,
) (*azdext.GetProjectResponse, error) {
	endpoint := ""
	if location := s.env.set[envKeyLocation]; location != "" {
		endpoint = "https://search." + location + ".example"
	}
	return &azdext.GetProjectResponse{Project: &azdext.ProjectConfig{
		Path: s.projectPath,
		Services: map[string]*azdext.ServiceConfig{
			"connection": {
				Environment: map[string]string{"ENDPOINT": endpoint},
			},
		},
	}}, nil
}

// newPromptOrderTestClient serves the project, environment and prompt
// stubs needed to exercise Initialize end to end.
func newPromptOrderTestClient(
	t *testing.T,
	projSrv azdext.ProjectServiceServer,
	envSrv azdext.EnvironmentServiceServer,
	promptSrv azdext.PromptServiceServer,
) *azdext.AzdClient {
	t.Helper()

	srv := grpc.NewServer()
	azdext.RegisterProjectServiceServer(srv, projSrv)
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

func TestInitializeResolvesEnvBeforeReadingServiceEnvironments(t *testing.T) {
	// Greenfield: neither AZURE_SUBSCRIPTION_ID nor AZURE_LOCATION is
	// set, so Initialize must prompt first. Reading service
	// environments before the prompt would synthesize the connection
	// with an empty target.
	projectPath := t.TempDir()
	require.NoError(t, os.WriteFile(
		filepath.Join(projectPath, "azure.yaml"),
		[]byte(`
services:
  project:
    host: azure.ai.project
  connection:
    host: azure.ai.connection
    uses: [project]
    env:
      ENDPOINT: ${SEARCH_ENDPOINT}
    category: CognitiveSearch
    target: ${ENDPOINT}
    authType: None
`),
		0o600,
	))

	env := &resolveEnvStubEnvServer{envName: "test", get: map[string]string{}}
	prompt := &resolveEnvStubPromptServer{
		subscriptionID: "00000000-0000-0000-0000-000000000001",
		location:       "westus2",
	}
	client := newPromptOrderTestClient(
		t,
		&promptOrderStubProjectServer{projectPath: projectPath, env: env},
		env,
		prompt,
	)
	provider := &FoundryProvisioningProvider{azdClient: client}

	err := provider.Initialize(
		t.Context(),
		projectPath,
		&azdext.ProvisioningOptions{Provider: FoundryProviderName},
	)
	require.NoError(t, err)
	assert.Equal(t, 1, prompt.subscriptionN)
	assert.Equal(t, 1, prompt.locationN)

	require.NotNil(t, provider.synthResult)
	connections, ok := provider.synthResult.Parameters["connections"].([]synthesis.Connection)
	require.True(t, ok)
	require.Len(t, connections, 1)
	assert.Equal(t, "https://search.westus2.example", connections[0].Target)
}

func TestInitializeValidatesConfigBeforePrompting(t *testing.T) {
	tests := []struct {
		name    string
		config  string
		wantErr string
	}{
		{
			name:    "invalid deployments",
			config:  "    deployments: invalid\n",
			wantErr: "decode service",
		},
		{
			name: "invalid network",
			config: "    network:\n" +
				"      peSubnet: {vnet: not-an-arm-id, name: pe}\n",
			wantErr: "not a well-formed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			projectPath := t.TempDir()
			require.NoError(t, os.WriteFile(
				filepath.Join(projectPath, "azure.yaml"),
				[]byte("services:\n"+
					"  project:\n"+
					"    host: azure.ai.project\n"+
					tt.config),
				0o600,
			))

			env := &resolveEnvStubEnvServer{
				envName: "test",
				get:     map[string]string{},
			}
			prompt := &resolveEnvStubPromptServer{
				subscriptionID: "sub-id",
				location:       "westus2",
			}
			client := newResolveEnvTestClient(t, env, prompt)
			provider := &FoundryProvisioningProvider{
				azdClient: client,
			}

			err := provider.Initialize(
				t.Context(),
				projectPath,
				&azdext.ProvisioningOptions{
					Provider: FoundryProviderName,
				},
			)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
			assert.Zero(t, prompt.subscriptionN)
			assert.Zero(t, prompt.locationN)
		})
	}
}

func TestInitializeProjectRefErrorUsesGenericMessage(t *testing.T) {
	projectPath := t.TempDir()
	require.NoError(t, os.WriteFile(
		filepath.Join(projectPath, "azure.yaml"),
		[]byte(`services:
  project:
    host: azure.ai.project
    $ref: missing.yaml
`),
		0o600,
	))

	env := &resolveEnvStubEnvServer{
		envName: "test",
		get:     map[string]string{},
	}
	prompt := &resolveEnvStubPromptServer{}
	client := newResolveEnvTestClient(t, env, prompt)
	provider := &FoundryProvisioningProvider{azdClient: client}

	err := provider.Initialize(
		t.Context(),
		projectPath,
		&azdext.ProvisioningOptions{Provider: FoundryProviderName},
	)
	require.Error(t, err)
	assert.Contains(
		t,
		err.Error(),
		"read Foundry project service configuration",
	)
	assert.NotContains(t, err.Error(), "existing Foundry project endpoint")
	assert.Zero(t, prompt.subscriptionN)
	assert.Zero(t, prompt.locationN)
}
