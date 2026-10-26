// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/azure/azure-dev/cli/azd/internal/grpcserver"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	v1beta "github.com/azure/azure-dev/cli/azd/pkg/azdext/contracts/v1beta"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/ext"
	"github.com/azure/azure-dev/cli/azd/pkg/extensions"
	"github.com/azure/azure-dev/cli/azd/pkg/infra"
	"github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning"
	"github.com/azure/azure-dev/cli/azd/pkg/lazy"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
)

// This crosses the core action, real core event server, and production Agents listen process.
// Provisioning and the host's supporting services are local fixtures.
func TestDownActionAgentsExtensionStateCleanup(t *testing.T) {
	if testing.Short() {
		t.Skip("builds and starts the first-party Agents extension")
	}
	buildCtx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
	defer cancel()
	binary := filepath.Join(t.TempDir(), "agents")
	if runtime.GOOS == "windows" {
		binary += ".exe"
	}
	//nolint:gosec // Builds repository-owned source into this test's temporary directory.
	build := exec.CommandContext(buildCtx, "go", "build", "-o", binary, ".")
	build.Dir = filepath.Join("..", "extensions", "azure.ai.agents")
	buildOutput, err := build.CombinedOutput()
	require.NoError(t, err, "%s", buildOutput)

	const endpoint = "https://acct.services.ai.azure.com/api/projects/project"
	const agent = "acct.services.ai.azure.com/api/projects/project/agents/echo"
	for _, test := range []struct {
		name                                          string
		failed, skipped, selected, partial, noJournal bool
		badProof                                      bool
	}{
		{name: "completed owned project"},
		{name: "already absent retry"},
		{name: "failed down", failed: true},
		{name: "skipped down", skipped: true},
		{name: "selected layer", selected: true},
		{name: "partial owned journal", partial: true},
		{name: "no BYO journal retains legacy version scope", noJournal: true},
		{name: "extension error prevents success", badProof: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			provider := &mockDownProvider{mockRefreshProvider: &mockRefreshProvider{},
				destroyResult: &provisioning.DestroyResult{SkippedDeletion: test.skipped}}
			if test.failed {
				provider.destroyErr = errors.New("provider failed before completion")
			} else if test.name == "already absent retry" {
				provider.destroyErr = infra.ErrDeploymentResourcesNotFound
			}
			action, console, manager := newTestDownAction(t, provider)
			manager.On("Reload", mock.Anything, mock.Anything).Return(nil)
			if test.selected {
				action.args = []string{"infra"}
			}
			action.projectConfig.Services = map[string]*project.ServiceConfig{
				"echo": {
					Name: "echo", Host: "azure.ai.agent", Project: action.projectConfig,
					EventDispatcher: ext.NewEventDispatcher[project.ServiceLifecycleEventArgs](),
				},
			}
			action.env.DotenvSet("AGENT_ECHO_ENDPOINT", endpoint+"/agents/echo/versions/2")
			action.env.DotenvSet("AGENT_ECHO_PROJECT_ENDPOINT", endpoint)
			// The global endpoint has already been invalidated by successful down.
			if !test.noJournal {
				proofEndpoint := endpoint
				if test.badProof {
					proofEndpoint = ""
				}
				journal, err := json.Marshal(map[string]any{
					"version": 2, "cleanupComplete": !test.partial,
					"foundry": map[string]any{
						"spec": map[string]any{"projectId": "/subscriptions/00000000-0000-0000-0000-000000000001/" +
							"resourceGroups/rg/providers/Microsoft.CognitiveServices/accounts/acct/projects/project"},
						"projectEndpoint": proofEndpoint, "projectDeleted": true, "accountDeleted": !test.partial,
					},
				})
				require.NoError(t, err)
				action.env.DotenvSet("AZD_FOUNDRY_BYO_AKS_PREPARATION", string(journal))
			}
			configDir := t.TempDir()
			cacheDir := filepath.Join(configDir, "state", "extensions", "azure.ai.agents", "agents")
			require.NoError(t, os.MkdirAll(cacheDir, 0o700))
			owned := []string{agent, agent + "/versions/1/remote", agent + "/versions/2/remote"}
			unrelated := []string{
				agent + "/versions/1/local",
				agent + "-other/versions/1/remote",
				"acct.services.ai.azure.com/api/projects/other/agents/echo/versions/1/remote",
			}
			original := map[string][]byte{}
			for _, key := range append(owned, unrelated...) {
				sum := sha256.Sum256([]byte(key))
				path := filepath.Join(cacheDir, hex.EncodeToString(sum[:])+".json")
				data, err := json.Marshal(map[string]any{
					"version": 1, "agentKey": key, "sessionId": "session-" + key, "conversationId": "conversation-" + key,
				})
				require.NoError(t, err)
				require.NoError(t, os.WriteFile(path, data, 0o600))
				original[key] = data
			}

			extension := &downListenFixture{ready: make(chan struct{})}
			eventServer := grpcserver.NewEventService(extension,
				lazy.NewLazy(func() (environment.Manager, error) { return manager, nil }),
				lazy.NewLazy(func() (*project.ProjectConfig, error) { return action.projectConfig, nil }),
				lazy.NewLazy(func() (*environment.Environment, error) { return action.env, nil }),
				grpcserver.NewFollowUpManager(), console)
			server := grpc.NewServer(grpc.StreamInterceptor(func(
				srv any, stream grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler,
			) error {
				claims := &extensions.ExtensionClaims{}
				claims.Subject = "azure.ai.agents"
				ctx := extensions.WithClaimsContext(stream.Context(), claims)
				return handler(srv, &downClaimsStream{ServerStream: stream, ctx: ctx})
			}))
			azdext.RegisterEventServiceServer(server, eventServer)
			azdext.RegisterEnvironmentServiceServer(server, &downEnvironmentFixture{env: action.env})
			azdext.RegisterExtensionServiceServer(server, extension)
			azdext.RegisterServiceTargetServiceServer(server, &downTargetFixture{})
			v1beta.RegisterServiceTargetServiceServer(server, &downBetaTargetFixture{})
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			require.NoError(t, err)
			go func() { _ = server.Serve(listener) }()
			t.Cleanup(server.Stop)
			t.Cleanup(func() { _ = listener.Close() })

			ctx, stop := context.WithTimeout(t.Context(), 30*time.Second)
			//nolint:gosec // The binary was built from repository-owned source above.
			process := exec.CommandContext(ctx, binary, "listen")
			process.Env = append(os.Environ(), "AZD_SERVER="+listener.Addr().String(), "AZD_ACCESS_TOKEN=test",
				"AZD_CONFIG_DIR="+configDir, "AZURE_DEV_COLLECT_TELEMETRY=no", "AZD_EXT_DEBUG=false", "AZD_DEBUG=false")
			var output bytes.Buffer
			process.Stdout, process.Stderr = &output, &output
			require.NoError(t, process.Start())
			exited := make(chan struct{})
			var processErr error
			go func() {
				processErr = process.Wait()
				close(exited)
			}()
			t.Cleanup(func() {
				stop()
				<-exited
				t.Logf("Agents listen output:\n%s", output.String())
			})
			select {
			case <-extension.ready:
			case <-exited:
				t.Fatalf("Agents extension exited before readiness: %v\n%s", processErr, output.String())
			case <-ctx.Done():
				t.Fatal("Agents extension did not register and signal readiness")
			}
			result, runErr := action.Run(ctx)
			if test.failed || test.badProof {
				require.Error(t, runErr)
				assert.Nil(t, result)
				if test.badProof {
					assert.ErrorContains(t, runErr, "deleted Project API endpoint is unverified")
				}
			} else {
				require.NoError(t, runErr)
			}
			for _, key := range append(owned, unrelated...) {
				sum := sha256.Sum256([]byte(key))
				path := filepath.Join(cacheDir, hex.EncodeToString(sum[:])+".json")
				deleted := !test.failed && !test.skipped && !test.selected && !test.partial && !test.badProof &&
					(key == agent || key == owned[2] || (key == owned[1] && !test.noJournal))
				if deleted {
					_, err := os.Stat(path)
					assert.ErrorIs(t, err, os.ErrNotExist, key)
				} else {
					//nolint:gosec // Reads a known hashed cache path under this test's temporary directory.
					actual, err := os.ReadFile(path)
					require.NoError(t, err, key)
					assert.Equal(t, original[key], actual, key)
				}
			}
		})
	}
}

type downClaimsStream struct {
	grpc.ServerStream
	ctx context.Context
}

func (s *downClaimsStream) Context() context.Context { return s.ctx }

type downListenFixture struct {
	azdext.UnimplementedExtensionServiceServer
	ready chan struct{}
	once  sync.Once
}

func (s *downListenFixture) GetInstalled(extensions.FilterOptions) (*extensions.Extension, error) {
	return &extensions.Extension{Id: "azure.ai.agents",
		Capabilities: []extensions.CapabilityType{extensions.LifecycleEventsCapability}}, nil
}

func (s *downListenFixture) Ready(context.Context, *azdext.ReadyRequest) (*azdext.ReadyResponse, error) {
	s.once.Do(func() { close(s.ready) })
	return &azdext.ReadyResponse{}, nil
}

type downEnvironmentFixture struct {
	azdext.UnimplementedEnvironmentServiceServer
	env *environment.Environment
}

func (s *downEnvironmentFixture) GetCurrent(context.Context, *azdext.EmptyRequest) (*azdext.EnvironmentResponse, error) {
	return &azdext.EnvironmentResponse{Environment: &azdext.Environment{Name: s.env.Name()}}, nil
}

func (s *downEnvironmentFixture) GetValue(_ context.Context, req *azdext.GetEnvRequest) (*azdext.KeyValueResponse, error) {
	return &azdext.KeyValueResponse{Value: s.env.Getenv(req.Key)}, nil
}

type downTargetFixture struct {
	azdext.UnimplementedServiceTargetServiceServer
}

func (*downTargetFixture) Stream(
	stream grpc.BidiStreamingServer[azdext.ServiceTargetMessage, azdext.ServiceTargetMessage],
) error {
	for {
		request, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		if err := stream.Send(&azdext.ServiceTargetMessage{
			RequestId: request.RequestId,
			MessageType: &azdext.ServiceTargetMessage_RegisterServiceTargetResponse{
				RegisterServiceTargetResponse: &azdext.RegisterServiceTargetResponse{},
			},
		}); err != nil {
			return err
		}
	}

}

type downBetaTargetFixture struct {
	v1beta.UnimplementedServiceTargetServiceServer
}

func (*downBetaTargetFixture) Stream(
	stream grpc.BidiStreamingServer[v1beta.ServiceTargetMessage, v1beta.ServiceTargetMessage],
) error {
	for {
		request, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		if err := stream.Send(&v1beta.ServiceTargetMessage{
			RequestId: request.RequestId,
			MessageType: &v1beta.ServiceTargetMessage_RegisterServiceTargetResponse{
				RegisterServiceTargetResponse: &v1beta.RegisterServiceTargetResponse{},
			},
		}); err != nil {
			return err
		}
	}
}
