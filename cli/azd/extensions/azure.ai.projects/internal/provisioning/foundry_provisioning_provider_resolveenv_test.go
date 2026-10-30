// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package provisioning

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"net"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"azure.ai.projects/internal/exterrors"
	"azure.ai.projects/internal/synthesis"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/bicep"
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
	get        map[string]string
	getErr     map[string]error
	set        map[string]string
	process    map[string]string
	config     map[string][]byte
	configErr  error
	currentErr error
	valuesErr  error
	valuesN    int
}

func (s *resolveEnvStubEnvServer) GetConfig(
	_ context.Context, req *azdext.GetConfigRequest,
) (*azdext.GetConfigResponse, error) {
	if s.configErr != nil {
		return nil, s.configErr
	}
	value, found := s.config[req.Path]
	return &azdext.GetConfigResponse{Value: value, Found: found}, nil
}

func (s *resolveEnvStubEnvServer) SetConfig(
	_ context.Context, req *azdext.SetConfigRequest,
) (*azdext.EmptyResponse, error) {
	if s.configErr != nil {
		return nil, s.configErr
	}
	if s.config == nil {
		s.config = map[string][]byte{}
	}
	s.config[req.Path] = req.Value
	return &azdext.EmptyResponse{}, nil
}

func (s *resolveEnvStubEnvServer) UnsetConfig(
	_ context.Context, req *azdext.UnsetConfigRequest,
) (*azdext.EmptyResponse, error) {
	if s.configErr != nil {
		return nil, s.configErr
	}
	delete(s.config, req.Path)
	return &azdext.EmptyResponse{}, nil
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
	value, exists := s.get[req.Key]
	if !exists {
		value = s.process[req.Key]
	}
	return &azdext.KeyValueResponse{Value: value}, nil
}

func (s *resolveEnvStubEnvServer) GetValues(
	_ context.Context, _ *azdext.GetEnvironmentRequest,
) (*azdext.KeyValueListResponse, error) {
	s.valuesN++
	if s.valuesErr != nil {
		return nil, s.valuesErr
	}
	values := make([]*azdext.KeyValue, 0, len(s.get))
	for key, value := range s.get {
		values = append(values, &azdext.KeyValue{Key: key, Value: value})
	}
	return &azdext.KeyValueListResponse{KeyValues: values}, nil
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

// newResolveEnvTestClient connects an AzdClient to the supplied host service stubs.
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

func TestInitializeValidatesPromptedRegistrySubscriptionBeforeWrites(t *testing.T) {
	t.Setenv(synthesis.ACRReuseEnvVar, "true")
	for _, layer := range []bool{false, true} {
		for _, onDisk := range []bool{false, true} {
			for _, canonical := range []bool{false, true} {
				for _, subscription := range []string{"sub", "other"} {
					t.Run(fmt.Sprintf("layer=%t/onDisk=%t/canonical=%t/%s", layer, onDisk, canonical, subscription), func(t *testing.T) {
						root := t.TempDir()
						raw := "services:\n  project:\n    host: azure.ai.project\n" +
							"  agent:\n    host: azure.ai.agent\n    kind: hosted\n"
						options := &azdext.ProvisioningOptions{Provider: FoundryProviderName}
						if layer {
							raw += "infra:\n  layers:\n    - name: foundry\n      provider: microsoft.foundry\n" +
								"      path: infra/foundry\n"
							options.Name, options.Path = "foundry", filepath.Join("infra", "foundry")
						}
						path := filepath.Join(root, "azure.yaml")
						require.NoError(t, os.WriteFile(path, []byte(raw), 0o600))
						if onDisk {
							infraPath := options.Path
							if infraPath == "" {
								infraPath = onDiskInfraDir
							}
							require.NoError(t, os.MkdirAll(filepath.Join(root, infraPath), 0o750))
							require.NoError(t, os.WriteFile(filepath.Join(root, infraPath, onDiskBicepFile),
								[]byte("// ejected Bicep"), 0o600))
						}
						env := &resolveEnvStubEnvServer{envName: "test", get: map[string]string{
							"AZURE_CONTAINER_REGISTRY_RESOURCE_ID": testACRID,
						}}
						if canonical {
							group := "rg-test"
							if layer {
								group += "-foundry"
							}
							binding, err := json.Marshal(synthesis.ACRBinding{
								Version: 1, Mode: "reuse-connect", RegistryResourceID: testACRID, PrincipalID: "principal",
								ProjectResourceID: "/subscriptions/sub/resourceGroups/" + group +
									"/providers/Microsoft.CognitiveServices/accounts/account/projects/test",
							})
							require.NoError(t, err)
							env.get = map[string]string{}
							env.config = map[string][]byte{synthesis.ACRBindingConfigKey: binding}
						}
						beforeConfig := maps.Clone(env.config)
						prompt := &resolveEnvStubPromptServer{subscriptionID: subscription, location: "eastus2"}
						p := &FoundryProvisioningProvider{azdClient: newResolveEnvTestClient(t, env, prompt)}
						p.acrRequest = func(context.Context, string, string, string, any) error {
							t.Fatal("initialization must not deploy or read Azure resources")
							return nil
						}
						err := p.Initialize(t.Context(), root, options)
						assert.Equal(t, 1, prompt.subscriptionN)
						if subscription == "sub" {
							require.NoError(t, err)
							assert.Equal(t, subscription, env.set[envKeySubscriptionID])
							assert.Equal(t, "eastus2", env.set[envKeyLocation])
							assert.Equal(t, 1, prompt.locationN)
							assert.Equal(t, "reuse-connect", p.greenfieldAcrMode)
							require.NotNil(t, p.synthResult)
							assert.Equal(t, testACRID, p.synthResult.Parameters["existingAcrResourceId"])
						} else {
							require.ErrorContains(t, err, "same subscription")
							assert.Empty(t, p.subID)
							assert.Empty(t, env.set)
							assert.Zero(t, prompt.locationN)
							assert.Nil(t, p.synthResult)
							assert.Nil(t, p.armTemplate)
						}
						assert.Equal(t, beforeConfig, env.config)
						assert.Empty(t, p.synthesisEnv[envKeySubscriptionID])
						//nolint:gosec // Test-owned path.
						persisted, readErr := os.ReadFile(path)
						require.NoError(t, readErr)
						assert.Equal(t, raw, string(persisted))
					})
				}
			}
		}
	}
}

func TestInitializeUsesPromptedLocationInSynthesis(t *testing.T) {
	t.Setenv(synthesis.ACRReuseEnvVar, "true")
	for _, layer := range []bool{false, true} {
		for _, onDisk := range []bool{false, true} {
			for _, blank := range []bool{false, true} {
				t.Run(fmt.Sprintf("layer=%t/onDisk=%t/blank=%t", layer, onDisk, blank), func(t *testing.T) {
					root := t.TempDir()
					vnetID := "/subscriptions/sub/resourceGroups/network-${AZURE_LOCATION}/" +
						"providers/Microsoft.Network/virtualNetworks/vnet"
					raw := "services:\n  project:\n    host: azure.ai.project\n    network:\n" +
						"      peSubnet: {vnet: \"" + vnetID + "\", name: pe}\n"
					options := &azdext.ProvisioningOptions{Provider: FoundryProviderName}
					if layer {
						raw += "infra:\n  layers:\n    - name: foundry\n      provider: microsoft.foundry\n" +
							"      path: infra/foundry\n"
						options.Name, options.Path = "foundry", filepath.Join("infra", "foundry")
					}
					require.NoError(t, os.WriteFile(filepath.Join(root, "azure.yaml"), []byte(raw), 0o600))
					if onDisk {
						path := options.Path
						if path == "" {
							path = onDiskInfraDir
						}
						require.NoError(t, os.MkdirAll(filepath.Join(root, path), 0o750))
						require.NoError(t, os.WriteFile(filepath.Join(root, path, onDiskBicepFile),
							[]byte("// ejected Bicep"), 0o600))
					}
					env := &resolveEnvStubEnvServer{envName: "test", get: map[string]string{
						envKeySubscriptionID:                   "sub",
						"AZURE_CONTAINER_REGISTRY_RESOURCE_ID": testACRID,
					}}
					if blank {
						env.get[envKeyLocation] = ""
					}
					prompt := &resolveEnvStubPromptServer{location: "westus2"}
					p := &FoundryProvisioningProvider{azdClient: newResolveEnvTestClient(t, env, prompt)}
					require.NoError(t, p.Initialize(t.Context(), root, options))
					assert.Equal(t, "/subscriptions/sub/resourceGroups/network-westus2/"+
						"providers/Microsoft.Network/virtualNetworks/vnet", p.synthResult.Parameters["vnetId"])
					assert.Equal(t, "westus2", p.location)
					assert.Equal(t, "westus2", env.set[envKeyLocation])
					assert.Equal(t, 1, prompt.locationN)
					assert.Equal(t, 1, env.valuesN)
					assert.Equal(t, "", p.synthesisEnv[envKeyLocation])
					assert.Equal(t, testACRID, p.synthesisEnv["AZURE_CONTAINER_REGISTRY_RESOURCE_ID"])
				})
			}
		}
	}
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

func TestResolveEnv_OptionalValueReadErrorsSurface(t *testing.T) {
	for _, key := range []string{envKeyFoundryRG, envKeyFoundryRGOwner, envKeyProjectName} {
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

// forbiddenProjectServer proves Initialize never asks core to expand service env.
type forbiddenProjectServer struct {
	azdext.UnimplementedProjectServiceServer
	calls atomic.Int32
}

func (s *forbiddenProjectServer) Get(
	context.Context, *azdext.EmptyRequest,
) (*azdext.GetProjectResponse, error) {
	s.calls.Add(1)
	return nil, status.Error(codes.Internal, "Projects must not request expanded service environments")
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

func TestInitializeLeavesSplitConnectionEnvironmentToOwningExtension(t *testing.T) {
	for _, mode := range []string{"greenfield", "brownfield-reuse", "brownfield-models"} {
		for _, onDisk := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/ondisk=%t", mode, onDisk), func(t *testing.T) {
				projectPath := t.TempDir()
				projectConfig := ""
				const endpoint = "https://account.services.ai.azure.com/api/projects/project"
				if mode != "greenfield" {
					projectConfig = "    endpoint: " + endpoint + "\n"
				}
				if mode == "brownfield-models" {
					projectConfig += "    deployments:\n" +
						"      - name: model\n" +
						"        model: {name: gpt-4.1-mini, format: OpenAI, version: '2025-04-14'}\n" +
						"        sku: {name: GlobalStandard, capacity: 1}\n"
				}
				// Missing file refs, an invalid payload and unresolvable env are
				// intentionally owned by Connections, never by Projects.
				raw := "services:\n  project:\n    host: azure.ai.project\n" + projectConfig + `
  connection:
    host: azure.ai.connection
    uses: [project]
    $ref: ./missing-connection.yaml
    env:
      $ref: ./missing-connection-env.yaml
    target: ${UNRESOLVED_CONNECTION_ENDPOINT}
    credentials: ${UNRESOLVED_CONNECTION_CREDENTIALS}
    category: {invalid: payload}
  isolated:
    host: azure.ai.connection
    env: {}
    target: ${UNRESOLVED_CONNECTION_ENDPOINT}
`
				require.NoError(t, os.WriteFile(filepath.Join(projectPath, "azure.yaml"), []byte(raw), 0o600))
				if onDisk {
					infraDir := filepath.Join(projectPath, onDiskInfraDir)
					require.NoError(t, os.MkdirAll(infraDir, 0o750))
					require.NoError(t, os.WriteFile(filepath.Join(infraDir, onDiskBicepFile), []byte("// project"), 0o600))
				}
				env := &resolveEnvStubEnvServer{envName: "test", get: map[string]string{
					"AZURE_AI_PROJECT_ID": "/subscriptions/00000000-0000-0000-0000-000000000001/" +
						"resourceGroups/rg/providers/Microsoft.CognitiveServices/accounts/account/projects/project",
					"FOUNDRY_PROJECT_ENDPOINT": endpoint,
				}}
				prompt := &resolveEnvStubPromptServer{
					subscriptionID: "00000000-0000-0000-0000-000000000001",
					location:       "westus2",
				}
				project := &forbiddenProjectServer{}
				client := newPromptOrderTestClient(t, project, env, prompt)
				provider := &FoundryProvisioningProvider{
					azdClient: client,
					bicepCliInstance: &stubCompiler{
						buildResult: bicep.BuildResult{Compiled: minimalARMTemplate()},
					},
				}

				require.NoError(t, provider.Initialize(t.Context(), projectPath,
					&azdext.ProvisioningOptions{Provider: FoundryProviderName}))
				assert.Zero(t, project.calls.Load())
				reuseOnly := mode == "brownfield-reuse" && !onDisk
				assert.Equal(t, reuseOnly, provider.existingProjectReuseOnly)
				wantPrompts := 1
				if reuseOnly {
					wantPrompts = 0
				}
				assert.Equal(t, wantPrompts, prompt.subscriptionN)
				assert.Equal(t, wantPrompts, prompt.locationN)
				require.NotNil(t, provider.synthResult)
				assert.NotContains(t, provider.synthResult.Parameters, "connections")
				assert.NotContains(t, provider.synthResult.Parameters, "connectionCredentials")
			})
		}
	}
}

func TestInitializeExistingRegistry(t *testing.T) {
	t.Setenv(synthesis.ACRReuseEnvVar, "true")
	const subscription = "11111111-1111-1111-1111-111111111111"
	for _, tt := range []struct {
		name     string
		group    string
		layer    bool
		explicit bool
		virtual  bool
		rejected bool
		mode     string
		noID     bool
		noAcr    bool
		invalid  string
		wantMode string
		wantErr  string
		onDisk   bool
		deployed bool
		bound    bool
	}{
		{name: "reuse", group: "shared"},
		{name: "on-disk reuse without recorded mode", group: "shared", onDisk: true},
		{name: "on-disk deployed create", mode: "create", group: "rg-test", wantMode: "create",
			onDisk: true, deployed: true},
		{name: "on-disk layer reuse", group: "shared", layer: true, onDisk: true},
		{name: "on-disk same group rejected before prompting", group: "custom", explicit: true,
			mode: "reuse-connect", rejected: true, onDisk: true},
		{name: "virtual env selector wins", group: "shared", virtual: true},
		{name: "default group rejected", group: "rg-test", mode: "reuse-connect", rejected: true},
		{name: "explicit group rejected before prompting", group: "custom", explicit: true,
			mode: "reuse-connect", rejected: true},
		{name: "layer can reuse root registry", group: "root", layer: true},
		{name: "layer default group rejected", group: "rg-test-foundry", layer: true,
			mode: "reuse-connect", rejected: true},
		{name: "fresh in-group ID without mode rejected", group: "rg-test", rejected: true},
		{name: "fresh layer in-group ID without mode rejected", group: "rg-test-foundry", layer: true, rejected: true},
		{name: "legacy deployment outputs preserve create", group: "rg-test", wantMode: "create", deployed: true},
		{name: "empty ID records create", noID: true, wantMode: "create"},
		{name: "create intent alone is not ownership", mode: "create", group: "rg-test", rejected: true},
		{name: "binding preserves created registry", group: "rg-test", wantMode: "create", bound: true},
		{name: "recorded reuse", mode: "reuse-connect", group: "shared"},
		{name: "recorded reuse requires ID", mode: "reuse-connect", noID: true,
			wantErr: "requires AZURE_CONTAINER_REGISTRY_RESOURCE_ID"},
		{name: "unsupported recorded mode", mode: "already-connected", wantErr: "AZD_FOUNDRY_ACR_MODE"},
		{name: "malformed ID", invalid: "registry", wantErr: "AZURE_CONTAINER_REGISTRY_RESOURCE_ID"},
		{name: "wrong resource type", invalid: "/subscriptions/sub/resourceGroups/shared/providers/Microsoft.Storage/accounts/a",
			wantErr: "AZURE_CONTAINER_REGISTRY_RESOURCE_ID"},
		{name: "no registry needed", group: "shared", noAcr: true, wantMode: "none"},
		{name: "project-only input cannot share teardown group", group: "rg-test", noAcr: true, rejected: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			raw := "services:\n  project:\n    host: azure.ai.project\n" +
				"  agent:\n    host: azure.ai.agent\n    kind: hosted\n"
			if tt.noAcr {
				raw = "services:\n  project:\n    host: azure.ai.project\n"
			}
			options := &azdext.ProvisioningOptions{Provider: FoundryProviderName}
			if tt.layer {
				raw += "infra:\n  layers:\n    - name: foundry\n      provider: microsoft.foundry\n      path: infra/foundry\n"
				options.Name = "foundry"
				options.Path = "infra/foundry"
			}
			require.NoError(t, os.WriteFile(filepath.Join(root, "azure.yaml"), []byte(raw), 0o600))
			if tt.onDisk {
				infraPath := options.Path
				if infraPath == "" {
					infraPath = onDiskInfraDir
				}
				require.NoError(t, os.MkdirAll(filepath.Join(root, infraPath), 0o750))
				require.NoError(t, os.WriteFile(
					filepath.Join(root, infraPath, onDiskBicepFile), []byte("// ejected Bicep"), 0o600))
			}
			id := "/subscriptions/" + subscription + "/resourceGroups/" + tt.group +
				"/providers/Microsoft.ContainerRegistry/registries/sharedregistry"
			if tt.noID {
				id = ""
			}
			if tt.invalid != "" {
				id = tt.invalid
			}
			values := map[string]string{
				"AZURE_SUBSCRIPTION_ID":                subscription,
				"AZURE_LOCATION":                       "eastus",
				"AZURE_ENV_NAME":                       "test",
				"AZURE_CONTAINER_REGISTRY_RESOURCE_ID": id,
				"AZD_FOUNDRY_ACR_MODE":                 tt.mode,
			}
			if tt.layer {
				values["AZURE_RESOURCE_GROUP"] = "root"
			}
			if tt.deployed {
				values["AZURE_AI_PROJECT_ID"] = "/subscriptions/" + subscription + "/resourceGroups/" + tt.group +
					"/providers/Microsoft.CognitiveServices/accounts/account/projects/project"
				values["AZURE_CONTAINER_REGISTRY_ENDPOINT"] = "sharedregistry.azurecr.io"
			}
			if tt.explicit {
				values["AZURE_RESOURCE_GROUP"] = tt.group
			}
			if tt.rejected || tt.wantErr != "" {
				delete(values, "AZURE_LOCATION")
			}
			if tt.virtual {
				options.VirtualEnv = map[string]string{"AZURE_CONTAINER_REGISTRY_RESOURCE_ID": id}
				values["AZURE_CONTAINER_REGISTRY_RESOURCE_ID"] = "invalid-overridden-selector"
			}
			env := &resolveEnvStubEnvServer{envName: "test", get: values}
			if tt.bound {
				data, err := json.Marshal(synthesis.ACRBinding{
					Version: 1, Mode: "create", RegistryResourceID: id, PrincipalID: "principal",
					ProjectResourceID: "/subscriptions/" + subscription + "/resourceGroups/" + tt.group +
						"/providers/Microsoft.CognitiveServices/accounts/account/projects/project",
				})
				require.NoError(t, err)
				env.config = map[string][]byte{synthesis.ACRBindingConfigKey: data}
			}
			prompt := &resolveEnvStubPromptServer{}
			provider := &FoundryProvisioningProvider{azdClient: newResolveEnvTestClient(t, env, prompt)}
			err := provider.Initialize(t.Context(), root, options)
			if tt.rejected {
				require.ErrorContains(t, err, "azd down deletes")
				assert.Empty(t, env.set)
			} else if tt.wantErr != "" {
				require.ErrorContains(t, err, tt.wantErr)
				assert.Empty(t, env.set)
			} else {
				require.NoError(t, err)
				wantMode := tt.wantMode
				if wantMode == "" {
					wantMode = "reuse-connect"
				}
				assert.NotContains(t, env.set, "AZD_FOUNDRY_ACR_MODE")
				assert.Equal(t, wantMode, provider.greenfieldAcrMode)
				if tt.onDisk {
					assert.Nil(t, provider.armTemplate, "on-disk initialization must not load the embedded template")
					return
				}
				params, err := provider.Parameters(t.Context())
				require.NoError(t, err)
				if wantMode == "reuse-connect" {
					assert.Equal(t, map[string]any{"value": id}, provider.armParameters()["existingAcrResourceId"])
					found := false
					for _, param := range params {
						if param.Name == "existingAcrResourceId" {
							found = true
							assert.Equal(t, []string{"AZURE_CONTAINER_REGISTRY_RESOURCE_ID"}, param.EnvVarMapping)
							assert.Equal(t, id, param.Value)
						}
					}
					assert.True(t, found)
				} else {
					assert.NotContains(t, provider.armParameters(), "existingAcrResourceId")
					for _, param := range params {
						assert.NotEqual(t, "existingAcrResourceId", param.Name)
					}
				}
			}
			assert.Zero(t, prompt.subscriptionN)
			assert.Zero(t, prompt.locationN)
		})
	}
}

func TestInitializeRegistryEnvironmentReadFailure(t *testing.T) {
	t.Setenv(synthesis.ACRReuseEnvVar, "true")
	for _, read := range []string{"current", "values"} {
		t.Run(read, func(t *testing.T) {
			root := t.TempDir()
			require.NoError(t, os.WriteFile(filepath.Join(root, "azure.yaml"), []byte(
				"services:\n  project:\n    host: azure.ai.project\n"+
					"  agent:\n    host: azure.ai.agent\n"), 0o600))
			env := &resolveEnvStubEnvServer{
				envName: "test",
				get: map[string]string{
					envKeySubscriptionID: "sub", envKeyLocation: "eastus",
					"AZURE_CONTAINER_REGISTRY_RESOURCE_ID": testACRID,
				},
			}
			readErr := status.Error(codes.Unavailable, "temporary environment read failure")
			if read == "current" {
				env.currentErr = readErr
			} else {
				env.valuesErr = readErr
			}
			prompt := &resolveEnvStubPromptServer{}
			provider := &FoundryProvisioningProvider{azdClient: newResolveEnvTestClient(t, env, prompt)}
			options := &azdext.ProvisioningOptions{Provider: FoundryProviderName}
			require.ErrorContains(t, provider.Initialize(t.Context(), root, options), "temporary environment read failure")
			assert.Nil(t, provider.synthResult)
			assert.Nil(t, provider.armTemplate)
			assert.Empty(t, provider.synthesisEnv)
			assert.Empty(t, env.set)
			assert.Empty(t, env.config)
			assert.Zero(t, prompt.subscriptionN)
			assert.Zero(t, prompt.locationN)

			env.currentErr, env.valuesErr = nil, nil
			env.valuesN = 0
			require.NoError(t, provider.Initialize(t.Context(), root, options))
			assert.Equal(t, "reuse-connect", provider.greenfieldAcrMode)
			assert.Equal(t, map[string]any{"value": testACRID}, provider.armParameters()["existingAcrResourceId"])
			assert.Equal(t, 1, env.valuesN, "template selection must use one complete snapshot")
		})
	}
}

func TestRegistryTemplateEnvironmentStaysPinned(t *testing.T) {
	env := &resolveEnvStubEnvServer{envName: "test", get: map[string]string{
		"AZURE_CONTAINER_REGISTRY_RESOURCE_ID": testACRID,
	}}
	p := &FoundryProvisioningProvider{azdClient: newResolveEnvTestClient(t, env, &resolveEnvStubPromptServer{})}
	_, err := p.synthesisEnvMap(t.Context())
	require.NoError(t, err)
	env.valuesErr = status.Error(codes.Unavailable, "later read must not replace the validated snapshot")
	values, err := p.envValues(t.Context())
	require.NoError(t, err)
	assert.Equal(t, testACRID, values["AZURE_CONTAINER_REGISTRY_RESOURCE_ID"])
	assert.Equal(t, 1, env.valuesN)
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
