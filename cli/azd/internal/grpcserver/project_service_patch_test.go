// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package grpcserver

import (
	"sync"
	"sync/atomic"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/lazy"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockenv"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/structpb"
)

func TestProjectService_PatchServiceConfig_Create(t *testing.T) {
	t.Parallel()

	svc := newProjectServiceWithYaml(t, "name: test-project\n")
	_, err := svc.PatchServiceConfig(t.Context(), &azdext.PatchServiceConfigRequest{
		ServiceName:     "nightly.summary",
		RequiredHost:    "azure.ai.routine",
		CreateIfMissing: true,
		ExpectedUses:    &azdext.StringListValue{},
		SetValues: map[string]*structpb.Value{
			"description": structpb.NewStringValue("nightly summary"),
			"uses": structpb.NewListValue(&structpb.ListValue{Values: []*structpb.Value{
				structpb.NewStringValue("agent-service"),
			}}),
		},
	})
	require.NoError(t, err)

	serviceConfig := loadPatchedService(t, svc, "nightly.summary")
	assert.Equal(t, "azure.ai.routine", serviceConfig["host"])
	assert.Equal(t, "nightly summary", serviceConfig["description"])
	assert.Equal(t, []any{"agent-service"}, serviceConfig["uses"])
}

func TestProjectService_PatchServiceConfig_MergesAndPreservesRawValues(t *testing.T) {
	t.Parallel()

	svc := newProjectServiceWithYaml(t, `name: test-project
services:
  nightly.summary:
    host: azure.ai.routine
    description: old
    env:
      TOPIC: ${TOPIC}
    custom: keep
    action:
      type: old
`)
	projectService := svc.(*projectService)
	before, err := projectService.lazyProjectConfig.GetValue()
	require.NoError(t, err)
	_, err = svc.PatchServiceConfig(t.Context(), &azdext.PatchServiceConfigRequest{
		ServiceName:  "nightly.summary",
		RequiredHost: "azure.ai.routine",
		SetValues: map[string]*structpb.Value{
			"description": structpb.NewStringValue("new"),
			"customNull":  structpb.NewNullValue(),
		},
		UnsetPaths: []string{"action"},
	})
	require.NoError(t, err)

	serviceConfig := loadPatchedService(t, svc, "nightly.summary")
	assert.Equal(t, "new", serviceConfig["description"])
	assert.Nil(t, serviceConfig["customNull"])
	assert.NotContains(t, serviceConfig, "action")
	assert.Equal(t, map[string]any{"TOPIC": "${TOPIC}"}, serviceConfig["env"])
	assert.Equal(t, "keep", serviceConfig["custom"])
	require.NotContains(t, loadServices(t, svc), "nightly")
	after, err := projectService.lazyProjectConfig.GetValue()
	require.NoError(t, err)
	require.Same(t, before.EventDispatcher, after.EventDispatcher)
	require.Same(t, before.Services["nightly.summary"].EventDispatcher, after.Services["nightly.summary"].EventDispatcher)
	assert.Equal(t, "new", after.Services["nightly.summary"].AdditionalProperties["description"])
}

func TestProjectService_PatchServiceConfig_SetWinsAfterUnset(t *testing.T) {
	t.Parallel()

	svc := newProjectServiceWithYaml(t, `name: test-project
services:
  api:
    host: appservice
    custom: old
`)
	_, err := svc.PatchServiceConfig(t.Context(), &azdext.PatchServiceConfigRequest{
		ServiceName:  "api",
		RequiredHost: "appservice",
		UnsetPaths:   []string{"custom"},
		SetValues: map[string]*structpb.Value{
			"custom": structpb.NewStringValue("new"),
		},
	})
	require.NoError(t, err)
	assert.Equal(t, "new", loadPatchedService(t, svc, "api")["custom"])
}

func TestProjectService_PatchServiceConfig_ExpectedUsesMatches(t *testing.T) {
	t.Parallel()

	svc := newProjectServiceWithYaml(t, `name: test-project
services:
  api:
    host: appservice
    uses:
      - connection
`)
	_, err := svc.PatchServiceConfig(t.Context(), &azdext.PatchServiceConfigRequest{
		ServiceName:  "api",
		RequiredHost: "appservice",
		ExpectedUses: &azdext.StringListValue{Values: []string{"connection"}},
		SetValues: map[string]*structpb.Value{
			"uses": structpb.NewListValue(&structpb.ListValue{Values: []*structpb.Value{
				structpb.NewStringValue("connection"),
				structpb.NewStringValue("agent"),
			}}),
		},
	})
	require.NoError(t, err)
	assert.Equal(t, []any{"connection", "agent"}, loadPatchedService(t, svc, "api")["uses"])
}

func TestProjectService_PatchServiceConfig_ExpectedUsesConflictLeavesConfigUnchanged(t *testing.T) {
	t.Parallel()

	svc := newProjectServiceWithYaml(t, `name: test-project
services:
  api:
    host: appservice
    uses:
      - connection
`)
	projectServer := svc.(*projectService)
	azdContext, err := projectServer.lazyAzdContext.GetValue()
	require.NoError(t, err)
	diskConfig, err := project.LoadConfig(t.Context(), azdContext.ProjectPath())
	require.NoError(t, err)
	require.NoError(t, diskConfig.Set("services.api.uses", []any{"connection", "concurrent-edit"}))
	require.NoError(t, project.SaveConfig(t.Context(), diskConfig, azdContext.ProjectPath()))

	_, err = svc.PatchServiceConfig(t.Context(), &azdext.PatchServiceConfigRequest{
		ServiceName:  "api",
		RequiredHost: "appservice",
		ExpectedUses: &azdext.StringListValue{Values: []string{"connection"}},
		SetValues: map[string]*structpb.Value{
			"uses": structpb.NewListValue(&structpb.ListValue{Values: []*structpb.Value{
				structpb.NewStringValue("connection"),
				structpb.NewStringValue("agent"),
			}}),
		},
	})
	require.Equal(t, codes.Aborted, status.Code(err))
	assert.Equal(t, []any{"connection", "concurrent-edit"}, loadPatchedService(t, svc, "api")["uses"])
}

func TestProjectService_PatchServiceConfig_ConcurrentMerges(t *testing.T) {
	t.Parallel()

	svc := newProjectServiceWithYaml(t, `name: test-project
services:
  api:
    host: appservice
`)
	projectService := svc.(*projectService)
	secondLockAttempted := make(chan struct{})
	projectService.configMutationMu = &notifyingLocker{
		Locker:     projectService.configMutationMu,
		secondLock: secondLockAttempted,
	}
	firstLoaded := make(chan struct{})
	releaseFirst := make(chan struct{})
	var loadCount atomic.Int32
	projectService.patchConfigLoaded = func() {
		if loadCount.Add(1) == 1 {
			close(firstLoaded)
			<-releaseFirst
		}
	}
	defer func() {
		projectService.patchConfigLoaded = nil
	}()

	errCh := make(chan error, 2)
	var wg sync.WaitGroup
	patch := func(path string) {
		wg.Go(func() {
			_, err := svc.PatchServiceConfig(t.Context(), &azdext.PatchServiceConfigRequest{
				ServiceName:  "api",
				RequiredHost: "appservice",
				SetValues: map[string]*structpb.Value{
					path: structpb.NewStringValue(path),
				},
			})
			errCh <- err
		})
	}
	patch("custom.one")
	<-firstLoaded
	patch("custom.two")
	<-secondLockAttempted
	close(releaseFirst)
	wg.Wait()
	close(errCh)
	for err := range errCh {
		require.NoError(t, err)
	}

	serviceConfig := config.NewConfig(loadPatchedService(t, svc, "api"))
	for _, path := range []string{"custom.one", "custom.two"} {
		value, found := serviceConfig.Get(path)
		require.True(t, found)
		assert.Equal(t, path, value)
	}
}

func TestProjectService_PatchServiceConfig_SerializesWithComposeService(t *testing.T) {
	t.Parallel()

	secondLockAttempted := make(chan struct{})
	sharedLock := &notifyingLocker{
		Locker:     NewProjectConfigMutationLocker(),
		secondLock: secondLockAttempted,
	}
	projectSvc := newProjectServiceWithYamlAndLock(t, `name: test-project
services:
  api:
    host: appservice
`, sharedLock)
	projectServer := projectSvc.(*projectService)

	firstLoaded := make(chan struct{})
	releaseFirst := make(chan struct{})
	projectServer.patchConfigLoaded = func() {
		close(firstLoaded)
		<-releaseFirst
	}
	defer func() { projectServer.patchConfigLoaded = nil }()

	env := environment.New("test")
	composeSvc := NewComposeServiceWithLock(
		projectServer.lazyAzdContext,
		lazy.From(env),
		lazy.From[environment.Manager](&mockenv.MockEnvManager{}),
		sharedLock,
	)

	patchErr := make(chan error, 1)
	go func() {
		_, err := projectSvc.PatchServiceConfig(t.Context(), &azdext.PatchServiceConfigRequest{
			ServiceName:  "api",
			RequiredHost: "appservice",
			SetValues: map[string]*structpb.Value{
				"description": structpb.NewStringValue("patched"),
			},
		})
		patchErr <- err
	}()
	<-firstLoaded

	composeErr := make(chan error, 1)
	go func() {
		_, err := composeSvc.AddResource(t.Context(), &azdext.AddResourceRequest{
			Resource: &azdext.ComposedResource{
				Name:   "storage",
				Type:   "Storage",
				Config: []byte("{}"),
			},
		})
		composeErr <- err
	}()
	<-secondLockAttempted
	close(releaseFirst)

	require.NoError(t, <-patchErr)
	require.NoError(t, <-composeErr)
	assert.Equal(t, "patched", loadPatchedService(t, projectSvc, "api")["description"])

	azdContext, err := projectServer.lazyAzdContext.GetValue()
	require.NoError(t, err)
	projectConfig, err := project.Load(t.Context(), azdContext.ProjectPath())
	require.NoError(t, err)
	require.Contains(t, projectConfig.Resources, "storage")
}

type notifyingLocker struct {
	sync.Locker
	lockCount  atomic.Int32
	secondLock chan struct{}
}

func (l *notifyingLocker) Lock() {
	if l.lockCount.Add(1) == 2 {
		close(l.secondLock)
	}
	l.Locker.Lock()
}

func TestProjectService_PatchServiceConfig_ErrorsLeaveConfigUnchanged(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		req  *azdext.PatchServiceConfigRequest
		code codes.Code
	}{
		{name: "nil request", code: codes.InvalidArgument},
		{
			name: "empty name",
			req:  &azdext.PatchServiceConfigRequest{RequiredHost: "appservice"},
			code: codes.InvalidArgument,
		},
		{name: "empty host", req: &azdext.PatchServiceConfigRequest{ServiceName: "api"}, code: codes.InvalidArgument},
		{
			name: "missing service",
			req: &azdext.PatchServiceConfigRequest{
				ServiceName:  "missing",
				RequiredHost: "appservice",
			},
			code: codes.NotFound,
		},
		{
			name: "host mismatch",
			req: &azdext.PatchServiceConfigRequest{
				ServiceName:  "api",
				RequiredHost: "containerapp",
			},
			code: codes.FailedPrecondition,
		},
		{
			name: "patch host",
			req: &azdext.PatchServiceConfigRequest{
				ServiceName:  "api",
				RequiredHost: "appservice",
				SetValues: map[string]*structpb.Value{
					"host": structpb.NewStringValue("containerapp"),
				},
			},
			code: codes.InvalidArgument,
		},
		{
			name: "patch language",
			req: &azdext.PatchServiceConfigRequest{
				ServiceName:  "api",
				RequiredHost: "appservice",
				SetValues: map[string]*structpb.Value{
					"language": structpb.NewStringValue("go"),
				},
			},
			code: codes.InvalidArgument,
		},
		{
			name: "unset host child",
			req: &azdext.PatchServiceConfigRequest{
				ServiceName:  "api",
				RequiredHost: "appservice",
				UnsetPaths:   []string{"host.name"},
			},
			code: codes.InvalidArgument,
		},
		{
			name: "empty path segment",
			req: &azdext.PatchServiceConfigRequest{
				ServiceName:  "api",
				RequiredHost: "appservice",
				UnsetPaths:   []string{"custom..value"},
			},
			code: codes.InvalidArgument,
		},
		{
			name: "nil value",
			req: &azdext.PatchServiceConfigRequest{
				ServiceName:  "api",
				RequiredHost: "appservice",
				SetValues: map[string]*structpb.Value{
					"custom": nil,
				},
			},
			code: codes.InvalidArgument,
		},
		{
			name: "typed null",
			req: &azdext.PatchServiceConfigRequest{
				ServiceName:  "api",
				RequiredHost: "appservice",
				SetValues: map[string]*structpb.Value{
					"uses": structpb.NewNullValue(),
				},
			},
			code: codes.InvalidArgument,
		},
		{
			name: "nested typed null",
			req: &azdext.PatchServiceConfigRequest{
				ServiceName:  "api",
				RequiredHost: "appservice",
				SetValues: map[string]*structpb.Value{
					"uses": structpb.NewListValue(&structpb.ListValue{
						Values: []*structpb.Value{structpb.NewNullValue()},
					}),
				},
			},
			code: codes.InvalidArgument,
		},
		{
			name: "overlapping sets",
			req: &azdext.PatchServiceConfigRequest{
				ServiceName:  "api",
				RequiredHost: "appservice",
				SetValues: map[string]*structpb.Value{
					"custom":       structpb.NewStructValue(&structpb.Struct{}),
					"custom.value": structpb.NewStringValue("x"),
				},
			},
			code: codes.InvalidArgument,
		},
		{
			name: "invalid service schema",
			req: &azdext.PatchServiceConfigRequest{
				ServiceName:  "api",
				RequiredHost: "appservice",
				SetValues: map[string]*structpb.Value{
					"uses": structpb.NewStringValue("not-a-list"),
				},
			},
			code: codes.InvalidArgument,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			svc := newProjectServiceWithYaml(t, `name: test-project
services:
  api:
    host: appservice
    description: unchanged
`)
			_, err := svc.PatchServiceConfig(t.Context(), test.req)
			require.Equal(t, test.code, status.Code(err))
			assert.Equal(t, "unchanged", loadPatchedService(t, svc, "api")["description"])
		})
	}
}

func loadPatchedService(t *testing.T, svc azdext.ProjectServiceServer, serviceName string) map[string]any {
	t.Helper()
	services := loadServices(t, svc)
	serviceConfig, ok := services[serviceName].(map[string]any)
	require.True(t, ok)
	return serviceConfig
}

func loadServices(t *testing.T, svc azdext.ProjectServiceServer) map[string]any {
	t.Helper()
	projectService := svc.(*projectService)
	azdContext, err := projectService.lazyAzdContext.GetValue()
	require.NoError(t, err)
	cfg, err := project.LoadConfig(t.Context(), azdContext.ProjectPath())
	require.NoError(t, err)
	services, found := cfg.GetMap("services")
	require.True(t, found)
	return services
}
