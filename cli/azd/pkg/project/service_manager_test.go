// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/azure/azure-dev/cli/azd/pkg/alpha"
	"github.com/azure/azure-dev/cli/azd/pkg/async"
	"github.com/azure/azure-dev/cli/azd/pkg/azapi"
	"github.com/azure/azure-dev/cli/azd/pkg/azure"
	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/ext"
	"github.com/azure/azure-dev/cli/azd/pkg/infra"
	"github.com/azure/azure-dev/cli/azd/pkg/ioc"
	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockarmresources"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockazapi"
)

type contextKey string

const (
	ServiceLanguageFake ServiceLanguageKind = "fake-framework"
	ServiceTargetFake   ServiceTargetKind   = "fake-service-target"

	frameworkRestoreCalled     contextKey = "frameworkRestoreCalled"
	frameworkBuildCalled       contextKey = "frameworkBuildCalled"
	frameworkPackageCalled     contextKey = "frameworkPackageCalled"
	serviceTargetPackageCalled contextKey = "serviceTargetPackageCalled"
	serviceTargetDeployCalled  contextKey = "serviceTargetDeployCalled"
	serviceTargetPublishCalled contextKey = "serviceTargetPublishCalled"
)

func createServiceManager(
	mockContext *mocks.MockContext,
	env *environment.Environment,
	operationCache ServiceOperationCache,
) ServiceManager {
	deploymentService := mockazapi.NewStandardDeploymentsFromMockContext(mockContext)
	resourceService := azapi.NewResourceService(mockContext.SubscriptionCredentialProvider, mockContext.ArmClientOptions)
	azureResourceManager := infra.NewAzureResourceManager(resourceService, deploymentService)
	resourceManager := NewResourceManager(env, deploymentService, resourceService, azureResourceManager)

	alphaManager := alpha.NewFeaturesManagerWithConfig(config.NewConfig(
		map[string]any{
			"alpha": map[string]any{
				"all": "on",
			},
		}))

	return NewServiceManager(env, resourceManager, mockContext.Container, operationCache, alphaManager)
}

func Test_ServiceManager_GetRequiredTools(t *testing.T) {
	mockContext := mocks.NewMockContext(t.Context())
	setupMocksForServiceManager(mockContext)
	env := environment.New("test")
	sm := createServiceManager(mockContext, env, ServiceOperationCache{})
	serviceConfig := createTestServiceConfig("./src/api", ServiceTargetFake, ServiceLanguageFake)
	tools, err := sm.GetRequiredTools(*mockContext.Context, serviceConfig)
	require.NoError(t, err)
	// Both require a tool, but only 1 unique tool
	require.Len(t, tools, 1)
}

func Test_ServiceManager_Initialize(t *testing.T) {
	mockContext := mocks.NewMockContext(t.Context())
	setupMocksForServiceManager(mockContext)
	env := environment.New("test")
	sm := createServiceManager(mockContext, env, ServiceOperationCache{})
	serviceConfig := createTestServiceConfig("./src/api", ServiceTargetFake, ServiceLanguageFake)

	err := sm.Initialize(*mockContext.Context, serviceConfig)
	require.NoError(t, err)
}

func Test_ServiceManager_Restore(t *testing.T) {
	mockContext := mocks.NewMockContext(t.Context())
	setupMocksForServiceManager(mockContext)
	env := environment.New("test")
	sm := createServiceManager(mockContext, env, ServiceOperationCache{})
	serviceConfig := createTestServiceConfig("./src/api", ServiceTargetFake, ServiceLanguageFake)

	raisedPreRestoreEvent := false
	raisedPostRestoreEvent := false

	_ = serviceConfig.AddHandler(
		*mockContext.Context,
		"prerestore",
		func(ctx context.Context, args ServiceLifecycleEventArgs) error {
			raisedPreRestoreEvent = true
			return nil
		})

	_ = serviceConfig.AddHandler(
		*mockContext.Context,
		"postrestore",
		func(ctx context.Context, args ServiceLifecycleEventArgs) error {
			raisedPostRestoreEvent = true
			return nil
		})

	restoreCalled := new(false)
	ctx := context.WithValue(*mockContext.Context, frameworkRestoreCalled, restoreCalled)
	result, err := logProgress(t, func(progess *async.Progress[ServiceProgress]) (*ServiceRestoreResult, error) {
		serviceContext := NewServiceContext()
		return sm.Restore(ctx, serviceConfig, serviceContext, progess)
	})

	require.NoError(t, err)
	require.NotNil(t, result)
	require.True(t, *restoreCalled)
	require.True(t, raisedPreRestoreEvent)
	require.True(t, raisedPostRestoreEvent)
}

func Test_ServiceManager_Build(t *testing.T) {
	mockContext := mocks.NewMockContext(t.Context())
	setupMocksForServiceManager(mockContext)
	env := environment.New("test")
	sm := createServiceManager(mockContext, env, ServiceOperationCache{})
	serviceConfig := createTestServiceConfig("./src/api", ServiceTargetFake, ServiceLanguageFake)

	raisedPreBuildEvent := false
	raisedPostBuildEvent := false

	_ = serviceConfig.AddHandler(
		*mockContext.Context,
		"prebuild",
		func(ctx context.Context, args ServiceLifecycleEventArgs) error {
			raisedPreBuildEvent = true
			return nil
		})

	_ = serviceConfig.AddHandler(
		*mockContext.Context,
		"postbuild",
		func(ctx context.Context, args ServiceLifecycleEventArgs) error {
			raisedPostBuildEvent = true
			return nil
		})

	buildCalled := new(false)
	ctx := context.WithValue(*mockContext.Context, frameworkBuildCalled, buildCalled)

	result, err := logProgress(t, func(progress *async.Progress[ServiceProgress]) (*ServiceBuildResult, error) {
		return sm.Build(ctx, serviceConfig, nil, progress)
	})

	require.NoError(t, err)
	require.NotNil(t, result)
	require.True(t, *buildCalled)
	require.True(t, raisedPreBuildEvent)
	require.True(t, raisedPostBuildEvent)
}

func Test_ServiceManager_Package(t *testing.T) {
	mockContext := mocks.NewMockContext(t.Context())
	setupMocksForServiceManager(mockContext)
	env := environment.New("test")
	sm := createServiceManager(mockContext, env, ServiceOperationCache{})
	serviceConfig := createTestServiceConfig("./src/api", ServiceTargetFake, ServiceLanguageFake)

	raisedPrePackageEvent := false
	raisedPostPackageEvent := false

	_ = serviceConfig.AddHandler(
		*mockContext.Context,
		"prepackage",
		func(ctx context.Context, args ServiceLifecycleEventArgs) error {
			raisedPrePackageEvent = true
			return nil
		})

	_ = serviceConfig.AddHandler(
		*mockContext.Context,
		"postpackage",
		func(ctx context.Context, args ServiceLifecycleEventArgs) error {
			raisedPostPackageEvent = true
			return nil
		})

	fakeFrameworkPackageCalled := new(false)
	fakeServiceTargetPackageCalled := new(false)
	ctx := context.WithValue(*mockContext.Context, frameworkPackageCalled, fakeFrameworkPackageCalled)
	ctx = context.WithValue(ctx, serviceTargetPackageCalled, fakeServiceTargetPackageCalled)

	result, err := logProgress(t, func(progress *async.Progress[ServiceProgress]) (*ServicePackageResult, error) {
		return sm.Package(ctx, serviceConfig, nil, progress, nil)
	})

	require.NoError(t, err)
	require.NotNil(t, result)
	require.True(t, *fakeFrameworkPackageCalled)
	require.True(t, *fakeServiceTargetPackageCalled)
	require.True(t, raisedPrePackageEvent)
	require.True(t, raisedPostPackageEvent)
}

func Test_ServiceManager_Deploy(t *testing.T) {
	mockContext := mocks.NewMockContext(t.Context())
	setupMocksForServiceManager(mockContext)
	env := environment.NewWithValues("test", map[string]string{
		environment.SubscriptionIdEnvVarName: "SUBSCRIPTION_ID",
	})
	sm := createServiceManager(mockContext, env, ServiceOperationCache{})
	serviceConfig := createTestServiceConfig("./src/api", ServiceTargetFake, ServiceLanguageFake)

	raisedPreDeployEvent := false
	raisedPostDeployEvent := false

	_ = serviceConfig.AddHandler(
		*mockContext.Context,
		"predeploy",
		func(ctx context.Context, args ServiceLifecycleEventArgs) error {
			raisedPreDeployEvent = true
			return nil
		})

	_ = serviceConfig.AddHandler(
		*mockContext.Context,
		"postdeploy",
		func(ctx context.Context, args ServiceLifecycleEventArgs) error {
			raisedPostDeployEvent = true
			return nil
		})

	deployCalled := new(false)
	ctx := context.WithValue(*mockContext.Context, serviceTargetDeployCalled, deployCalled)

	result, err := logProgress(t, func(progess *async.Progress[ServiceProgress]) (*ServiceDeployResult, error) {
		serviceContext := NewServiceContext()
		return sm.Deploy(ctx, serviceConfig, serviceContext, progess)
	})

	require.NoError(t, err)
	require.NotNil(t, result)
	require.True(t, *deployCalled)
	require.True(t, raisedPreDeployEvent)
	require.True(t, raisedPostDeployEvent)
}

func Test_ServiceManager_Publish(t *testing.T) {
	mockContext := mocks.NewMockContext(t.Context())
	setupMocksForServiceManager(mockContext)
	env := environment.NewWithValues("test", map[string]string{
		environment.SubscriptionIdEnvVarName: "SUBSCRIPTION_ID",
	})
	sm := createServiceManager(mockContext, env, ServiceOperationCache{})
	serviceConfig := createTestServiceConfig("./src/api", ServiceTargetFake, ServiceLanguageFake)

	raisedPrePublishEvent := false
	raisedPostPublishEvent := false

	_ = serviceConfig.AddHandler(
		*mockContext.Context,
		"prepublish",
		func(ctx context.Context, args ServiceLifecycleEventArgs) error {
			raisedPrePublishEvent = true
			return nil
		})

	_ = serviceConfig.AddHandler(
		*mockContext.Context,
		"postpublish",
		func(ctx context.Context, args ServiceLifecycleEventArgs) error {
			raisedPostPublishEvent = true
			return nil
		})

	publishCalled := new(false)
	ctx := context.WithValue(*mockContext.Context, serviceTargetPublishCalled, publishCalled)

	// Create a proper ServiceContext for the publish operation
	serviceContext := NewServiceContext()

	result, err := logProgress(t, func(progess *async.Progress[ServiceProgress]) (*ServicePublishResult, error) {
		return sm.Publish(ctx, serviceConfig, serviceContext, progess, nil)
	})

	require.NoError(t, err)
	require.NotNil(t, result)
	require.True(t, *publishCalled)
	require.True(t, raisedPrePublishEvent)
	require.True(t, raisedPostPublishEvent)
}

func Test_ServiceManager_GetFrameworkService(t *testing.T) {
	t.Run("Standard", func(t *testing.T) {
		mockContext := mocks.NewMockContext(t.Context())
		setupMocksForServiceManager(mockContext)
		env := environment.New("test")
		sm := createServiceManager(mockContext, env, ServiceOperationCache{})
		serviceConfig := createTestServiceConfig("./src/api", ServiceTargetFake, ServiceLanguageFake)

		framework, err := sm.GetFrameworkService(*mockContext.Context, serviceConfig)
		require.NoError(t, err)
		require.NotNil(t, framework)
		require.IsType(t, new(fakeFramework), framework)
	})

	t.Run("No project path and has docker tag", func(t *testing.T) {
		mockContext := mocks.NewMockContext(t.Context())
		mockContext.Container.MustRegisterNamedTransient("docker", newFakeFramework)

		setupMocksForServiceManager(mockContext)
		env := environment.New("test")
		sm := createServiceManager(mockContext, env, ServiceOperationCache{})
		serviceConfig := createTestServiceConfig("", ServiceTargetFake, ServiceLanguageNone)
		serviceConfig.Image = osutil.NewExpandableString("nginx")

		framework, err := sm.GetFrameworkService(*mockContext.Context, serviceConfig)
		require.NoError(t, err)
		require.NotNil(t, framework)
		require.IsType(t, new(fakeFramework), framework)
	})

	t.Run("No project path or docker tag", func(t *testing.T) {
		mockContext := mocks.NewMockContext(t.Context())
		mockContext.Container.MustRegisterNamedTransient("docker", newFakeFramework)

		setupMocksForServiceManager(mockContext)
		env := environment.New("test")
		sm := createServiceManager(mockContext, env, ServiceOperationCache{})
		serviceConfig := createTestServiceConfig("", ServiceTargetFake, ServiceLanguageNone)

		_, err := sm.GetFrameworkService(*mockContext.Context, serviceConfig)
		require.Error(t, err)
	})
}

func Test_ServiceManager_GetServiceTarget(t *testing.T) {
	mockContext := mocks.NewMockContext(t.Context())
	setupMocksForServiceManager(mockContext)
	env := environment.New("test")
	sm := createServiceManager(mockContext, env, ServiceOperationCache{})
	serviceConfig := createTestServiceConfig("./src/api", ServiceTargetFake, ServiceLanguageFake)

	serviceTarget, err := sm.GetServiceTarget(*mockContext.Context, serviceConfig)
	require.NoError(t, err)
	require.NotNil(t, serviceTarget)
	require.IsType(t, new(fakeServiceTarget), serviceTarget)
}

func Test_ServiceManager_GetServiceTarget_UnsupportedHost(t *testing.T) {
	mockContext := mocks.NewMockContext(t.Context())
	setupMocksForServiceManager(mockContext)
	env := environment.New("test")
	sm := createServiceManager(mockContext, env, ServiceOperationCache{})
	serviceConfig := createTestServiceConfig("./src/api", ServiceTargetKind("missing-target"), ServiceLanguageFake)

	_, err := sm.GetServiceTarget(*mockContext.Context, serviceConfig)
	require.Error(t, err)
	require.Contains(t, err.Error(), "service host 'missing-target' for service 'api' is unsupported")
}

func Test_ServiceManager_CacheResults(t *testing.T) {
	mockContext := mocks.NewMockContext(t.Context())
	setupMocksForServiceManager(mockContext)
	env := environment.New("test")
	sm := createServiceManager(mockContext, env, ServiceOperationCache{})
	serviceConfig := createTestServiceConfig("./src/api", ServiceTargetFake, ServiceLanguageFake)

	buildCalled := new(false)
	ctx := context.WithValue(*mockContext.Context, frameworkBuildCalled, buildCalled)

	buildResult1, _ := logProgress(
		t, func(progress *async.Progress[ServiceProgress]) (*ServiceBuildResult, error) {
			return sm.Build(ctx, serviceConfig, nil, progress)
		},
	)

	require.True(t, *buildCalled)
	*buildCalled = false

	buildResult2, _ := logProgress(
		t, func(progress *async.Progress[ServiceProgress]) (*ServiceBuildResult, error) {
			return sm.Build(ctx, serviceConfig, nil, progress)
		},
	)

	require.False(t, *buildCalled)
	require.Same(t, buildResult1, buildResult2)
}

func Test_ServiceManager_CacheResults_Across_Instances(t *testing.T) {
	mockContext := mocks.NewMockContext(t.Context())
	setupMocksForServiceManager(mockContext)
	env := environment.New("test")

	operationCache := ServiceOperationCache{}

	sm1 := createServiceManager(mockContext, env, operationCache)
	serviceConfig := createTestServiceConfig("./src/api", ServiceTargetFake, ServiceLanguageFake)

	packageCalled := new(false)
	ctx := context.WithValue(*mockContext.Context, serviceTargetPackageCalled, packageCalled)

	packageResult1, _ := logProgress(
		t, func(progress *async.Progress[ServiceProgress]) (*ServicePackageResult, error) {
			return sm1.Package(ctx, serviceConfig, nil, progress, nil)
		},
	)

	require.True(t, *packageCalled)
	*packageCalled = false

	sm2 := createServiceManager(mockContext, env, operationCache)
	packageResult2, _ := logProgress(
		t, func(progress *async.Progress[ServiceProgress]) (*ServicePackageResult, error) {
			return sm2.Package(ctx, serviceConfig, nil, progress, nil)
		},
	)

	require.False(t, *packageCalled)
	require.Same(t, packageResult1, packageResult2)
}

func Test_ServiceManager_Events_With_Errors(t *testing.T) {
	tests := []struct {
		name      string
		eventName string
		run       func(ctx context.Context, serviceManager ServiceManager, serviceConfig *ServiceConfig) (any, error)
	}{
		{
			name: "restore",
			run: func(ctx context.Context, serviceManager ServiceManager, serviceConfig *ServiceConfig) (any, error) {
				return logProgress(
					t, func(progess *async.Progress[ServiceProgress]) (*ServiceRestoreResult, error) {
						serviceContext := NewServiceContext()
						return serviceManager.Restore(ctx, serviceConfig, serviceContext, progess)
					})
			},
		},
		{
			name: "build",
			run: func(ctx context.Context, serviceManager ServiceManager, serviceConfig *ServiceConfig) (any, error) {
				return logProgress(
					t, func(progress *async.Progress[ServiceProgress]) (*ServiceBuildResult, error) {
						return serviceManager.Build(ctx, serviceConfig, nil, progress)
					})
			},
		},
		{
			name: "package",
			run: func(ctx context.Context, serviceManager ServiceManager, serviceConfig *ServiceConfig) (any, error) {
				return logProgress(
					t, func(progress *async.Progress[ServiceProgress]) (*ServicePackageResult, error) {
						return serviceManager.Package(ctx, serviceConfig, nil, progress, nil)
					})
			},
		},
		{
			name: "deploy",
			run: func(ctx context.Context, serviceManager ServiceManager, serviceConfig *ServiceConfig) (any, error) {
				return logProgress(
					t, func(progress *async.Progress[ServiceProgress]) (*ServiceDeployResult, error) {
						serviceContext := NewServiceContext()
						return serviceManager.Deploy(ctx, serviceConfig, serviceContext, progress)
					})
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			mockContext := mocks.NewMockContext(t.Context())
			setupMocksForServiceManager(mockContext)
			env := environment.NewWithValues("test", map[string]string{
				environment.SubscriptionIdEnvVarName: "SUBSCRIPTION_ID",
			})
			sm := createServiceManager(mockContext, env, ServiceOperationCache{})
			serviceConfig := createTestServiceConfig("./src/api", ServiceTargetFake, ServiceLanguageFake)

			eventTypes := []string{"pre", "post"}
			for _, eventType := range eventTypes {
				t.Run(test.eventName, func(t *testing.T) {
					test.eventName = eventType + test.name
					_ = serviceConfig.AddHandler(
						*mockContext.Context,
						ext.Event(test.eventName),
						func(ctx context.Context, args ServiceLifecycleEventArgs) error {
							return errors.New("error")
						},
					)

					result, err := test.run(*mockContext.Context, sm, serviceConfig)
					require.Error(t, err)
					require.Nil(t, result)
				})
			}
		})
	}
}

func setupMocksForServiceManager(mockContext *mocks.MockContext) {
	mockContext.Container.MustRegisterNamedSingleton(string(ServiceLanguageFake), newFakeFramework)
	mockContext.Container.MustRegisterNamedSingleton(string(ServiceTargetFake), newFakeServiceTarget)

	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(command, "fake-framework restore")
	}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
		return exec.NewRunResult(0, "", ""), nil
	})

	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(command, "fake-framework build")
	}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
		return exec.NewRunResult(0, "", ""), nil
	})

	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(command, "fake-framework package")
	}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
		return exec.NewRunResult(0, "", ""), nil
	})

	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(command, "fake-service-target package")
	}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
		return exec.NewRunResult(0, "", ""), nil
	})

	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(command, "fake-service-target deploy")
	}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
		return exec.NewRunResult(0, "", ""), nil
	})

	mockarmresources.AddResourceGroupListMock(mockContext.HttpClient, "SUBSCRIPTION_ID", []*armresources.ResourceGroup{
		{
			ID:       new("ID"),
			Name:     new("RESOURCE_GROUP"),
			Location: new("eastus2"),
			Type:     to.Ptr(string(azapi.AzureResourceTypeResourceGroup)),
		},
	})

	mockarmresources.AddAzResourceListMock(
		mockContext.HttpClient,
		new("RESOURCE_GROUP"),
		[]*armresources.GenericResourceExpanded{
			{
				ID:       new("ID"),
				Name:     new("WEB_APP"),
				Location: new("eastus2"),
				Type:     to.Ptr(string(azapi.AzureResourceTypeWebSite)),
				Tags: map[string]*string{
					azure.TagKeyAzdServiceName: new("api"),
				},
			},
		},
	)
}

// Fake implementation of framework service
type fakeFramework struct {
	commandRunner exec.CommandRunner
}

func newFakeFramework(commandRunner exec.CommandRunner) FrameworkService {
	return &fakeFramework{
		commandRunner: commandRunner,
	}
}

func (f *fakeFramework) Requirements() FrameworkRequirements {
	return FrameworkRequirements{
		Package: FrameworkPackageRequirements{
			RequireRestore: false,
			RequireBuild:   false,
		},
	}
}

func (f *fakeFramework) RequiredExternalTools(_ context.Context, _ *ServiceConfig) []tools.ExternalTool {
	return []tools.ExternalTool{&fakeTool{}}
}

func (f *fakeFramework) Initialize(ctx context.Context, serviceConfig *ServiceConfig) error {
	return nil
}

func (f *fakeFramework) Restore(
	ctx context.Context,
	serviceConfig *ServiceConfig,
	serviceContext *ServiceContext,
	_ *async.Progress[ServiceProgress],
) (*ServiceRestoreResult, error) {
	restoreCalled, ok := ctx.Value(frameworkRestoreCalled).(*bool)
	if ok {
		*restoreCalled = true
	}

	runArgs := exec.NewRunArgs("fake-framework", "restore")
	result, err := f.commandRunner.Run(ctx, runArgs)
	if err != nil {
		return nil, err
	}

	return &ServiceRestoreResult{
		Artifacts: ArtifactCollection{
			{
				Kind:         ArtifactKindDirectory,
				Location:     "/fake/restore/path",
				LocationKind: LocationKindLocal,
				Metadata: map[string]string{
					"command": result.Stdout,
				},
			},
		},
	}, nil
}

func (f *fakeFramework) Build(
	ctx context.Context,
	serviceConfig *ServiceConfig,
	serviceContext *ServiceContext,
	_ *async.Progress[ServiceProgress],
) (*ServiceBuildResult, error) {
	buildCalled, ok := ctx.Value(frameworkBuildCalled).(*bool)
	if ok {
		*buildCalled = true
	}

	runArgs := exec.NewRunArgs("fake-framework", "build")
	result, err := f.commandRunner.Run(ctx, runArgs)
	if err != nil {
		return nil, err
	}

	return &ServiceBuildResult{
		Artifacts: ArtifactCollection{
			{
				Kind:         ArtifactKindDirectory,
				Location:     "/fake/build/path",
				LocationKind: LocationKindLocal,
				Metadata: map[string]string{
					"command": result.Stdout,
				},
			},
		},
	}, nil
}

func (f *fakeFramework) Package(
	ctx context.Context,
	serviceConfig *ServiceConfig,
	serviceContext *ServiceContext,
	_ *async.Progress[ServiceProgress],
) (*ServicePackageResult, error) {
	packageCalled, ok := ctx.Value(frameworkPackageCalled).(*bool)
	if ok {
		*packageCalled = true
	}

	runArgs := exec.NewRunArgs("fake-framework", "package")
	result, err := f.commandRunner.Run(ctx, runArgs)
	if err != nil {
		return nil, err
	}

	return &ServicePackageResult{
		Artifacts: ArtifactCollection{
			{
				Kind:         ArtifactKindArchive,
				Location:     "/fake/package/path",
				LocationKind: LocationKindLocal,
				Metadata: map[string]string{
					"command": result.Stdout,
				},
			},
		},
	}, nil
}

// Fake implementation of a service target
type fakeServiceTarget struct {
	commandRunner exec.CommandRunner
}

func newFakeServiceTarget(commandRunner exec.CommandRunner) ServiceTarget {
	return &fakeServiceTarget{
		commandRunner: commandRunner,
	}
}

func (st *fakeServiceTarget) Initialize(ctx context.Context, serviceConfig *ServiceConfig) error {
	return nil
}

func (st *fakeServiceTarget) RequiredExternalTools(ctx context.Context, serviceConfig *ServiceConfig) []tools.ExternalTool {
	return []tools.ExternalTool{&fakeTool{}}
}

func (st *fakeServiceTarget) Package(
	ctx context.Context,
	serviceConfig *ServiceConfig,
	serviceContext *ServiceContext,
	progress *async.Progress[ServiceProgress],
) (*ServicePackageResult, error) {
	packageCalled, ok := ctx.Value(serviceTargetPackageCalled).(*bool)
	if ok {
		*packageCalled = true
	}

	runArgs := exec.NewRunArgs("fake-service-target", "package")
	result, err := st.commandRunner.Run(ctx, runArgs)
	if err != nil {
		return nil, err
	}

	return &ServicePackageResult{
		Artifacts: ArtifactCollection{
			{
				Kind:         ArtifactKindArchive,
				Location:     "/fake/service-target/package/path",
				LocationKind: LocationKindLocal,
				Metadata: map[string]string{
					"command": result.Stdout,
				},
			},
		},
	}, nil
}

func (st *fakeServiceTarget) Publish(
	ctx context.Context,
	serviceConfig *ServiceConfig,
	serviceContext *ServiceContext,
	targetResource *environment.TargetResource,
	progress *async.Progress[ServiceProgress],
	options *PublishOptions,
) (*ServicePublishResult, error) {
	publishCalled, ok := ctx.Value(serviceTargetPublishCalled).(*bool)
	if ok {
		*publishCalled = true
	}
	return &ServicePublishResult{
		Artifacts: ArtifactCollection{
			{
				Kind:         ArtifactKindEndpoint,
				Location:     "https://fake-published.azurewebsites.net",
				LocationKind: LocationKindRemote,
				Metadata: map[string]string{
					"imageHash": "sha256:fake123",
				},
			},
		},
	}, nil
}

func (st *fakeServiceTarget) Deploy(
	ctx context.Context,
	serviceConfig *ServiceConfig,
	serviceContext *ServiceContext,
	targetResource *environment.TargetResource,
	progress *async.Progress[ServiceProgress],
) (*ServiceDeployResult, error) {
	deployCalled, ok := ctx.Value(serviceTargetDeployCalled).(*bool)
	if ok {
		*deployCalled = true
	}

	runArgs := exec.NewRunArgs("fake-service-target", "deploy")
	result, err := st.commandRunner.Run(ctx, runArgs)
	if err != nil {
		return nil, err
	}

	return &ServiceDeployResult{
		Artifacts: ArtifactCollection{
			{
				Kind:         ArtifactKindDeployment,
				Location:     "https://fake-app.azurewebsites.net",
				LocationKind: LocationKindRemote,
				Metadata: map[string]string{
					"command": result.Stdout,
				},
			},
		},
	}, nil
}

func (st *fakeServiceTarget) Endpoints(
	ctx context.Context,
	serviceConfig *ServiceConfig,
	targetResource *environment.TargetResource,
) ([]string, error) {
	return []string{"https://test.azurewebsites.net"}, nil
}

type fakeTool struct {
}

func (t *fakeTool) CheckInstalled(ctx context.Context) error {
	return nil
}
func (t *fakeTool) InstallUrl() string {
	return "https://aka.ms"
}
func (t *fakeTool) Name() string {
	return "fake tool"
}

func Test_NewServiceManager(t *testing.T) {
	env := environment.NewWithValues("test", map[string]string{})
	container := ioc.NewNestedContainer(nil)
	cache := ServiceOperationCache{}
	afm := alpha.NewFeaturesManagerWithConfig(nil)

	sm := NewServiceManager(env, nil, container, cache, afm)
	require.NotNil(t, sm)
}

// helper to build a serviceManager for round 10 tests.
func makeServiceManager(
	env *environment.Environment,
	locator ioc.ServiceLocator,
	resMgr ResourceManager,
) *serviceManager {
	return &serviceManager{
		env:                 env,
		serviceLocator:      locator,
		resourceManager:     resMgr,
		operationCache:      ServiceOperationCache{},
		alphaFeatureManager: alpha.NewFeaturesManagerWithConfig(nil),
	}
}

func Test_ServiceManager_Package_CacheHit(t *testing.T) {
	env := environment.NewWithValues("test", map[string]string{})

	target := &fakeServiceTargetStub{packageResult: &ServicePackageResult{}}
	framework := NewNoOpProject(env)
	locator := &fakeServiceLocator{framework: framework, target: target}
	resMgr := &fakeResourceManager{
		targetResource: environment.NewTargetResource("sub", "rg", "res", "type"),
	}

	sm := makeServiceManager(env, locator, resMgr)
	svcConfig := makeSvcConfigWithDispatcher("web", ServiceLanguageJavaScript, AppServiceTarget, t.TempDir())

	// Pre-populate cache
	cached := &ServicePackageResult{}
	sm.setOperationResult(svcConfig, ServiceEventPackage, cached)

	progress := newDrainedProgress()
	defer progress.Done()

	result, err := sm.Package(t.Context(), svcConfig, nil, progress, nil)
	require.NoError(t, err)
	require.Same(t, cached, result) // should return cached instance
}

func Test_ServiceManager_Deploy_CacheHit(t *testing.T) {
	env := environment.NewWithValues("test", map[string]string{})

	target := &fakeServiceTargetStub{deployResult: &ServiceDeployResult{}}
	framework := NewNoOpProject(env)
	locator := &fakeServiceLocator{framework: framework, target: target}
	resMgr := &fakeResourceManager{
		targetResource: environment.NewTargetResource("sub", "rg", "res", "type"),
	}

	sm := makeServiceManager(env, locator, resMgr)
	svcConfig := makeSvcConfigWithDispatcher("web", ServiceLanguageJavaScript, AppServiceTarget, t.TempDir())

	// Pre-populate deploy cache
	cached := &ServiceDeployResult{}
	sm.setOperationResult(svcConfig, ServiceEventDeploy, cached)

	progress := newDrainedProgress()
	defer progress.Done()

	result, err := sm.Deploy(t.Context(), svcConfig, nil, progress)
	require.NoError(t, err)
	require.Same(t, cached, result)
}

func Test_ServiceManager_Publish_CacheHit(t *testing.T) {
	env := environment.NewWithValues("test", map[string]string{})

	target := &fakeServiceTargetStub{publishResult: &ServicePublishResult{}}
	framework := NewNoOpProject(env)
	locator := &fakeServiceLocator{framework: framework, target: target}
	resMgr := &fakeResourceManager{
		targetResource: environment.NewTargetResource("sub", "rg", "res", "type"),
	}

	sm := makeServiceManager(env, locator, resMgr)
	svcConfig := makeSvcConfigWithDispatcher("web", ServiceLanguageJavaScript, AppServiceTarget, t.TempDir())

	cached := &ServicePublishResult{}
	sm.setOperationResult(svcConfig, ServiceEventPublish, cached)

	progress := newDrainedProgress()
	defer progress.Done()

	result, err := sm.Publish(t.Context(), svcConfig, nil, progress, nil)
	require.NoError(t, err)
	require.Same(t, cached, result)
}

func Test_ServiceManager_Build_CacheHit(t *testing.T) {
	env := environment.NewWithValues("test", map[string]string{})

	framework := NewNoOpProject(env)
	locator := &fakeServiceLocator{framework: framework}
	resMgr := &fakeResourceManager{}

	sm := makeServiceManager(env, locator, resMgr)
	svcConfig := makeSvcConfigWithDispatcher("web", ServiceLanguageJavaScript, AppServiceTarget, t.TempDir())

	cached := &ServiceBuildResult{}
	sm.setOperationResult(svcConfig, ServiceEventBuild, cached)

	progress := newDrainedProgress()
	defer progress.Done()

	result, err := sm.Build(t.Context(), svcConfig, nil, progress)
	require.NoError(t, err)
	require.Same(t, cached, result)
}

func Test_ServiceManager_GetServiceTarget_IoC_Error(t *testing.T) {
	// When target resolution fails with ioc.ErrResolveInstance → ErrorWithSuggestion
	env := environment.NewWithValues("test", map[string]string{})
	locator := &fakeServiceLocator{} // target is nil → returns ioc.ErrResolveInstance
	sm := makeServiceManager(env, locator, &fakeResourceManager{})

	svcConfig := makeSvcConfigWithDispatcher("web", ServiceLanguageJavaScript, AppServiceTarget, t.TempDir())

	_, err := sm.GetServiceTarget(t.Context(), svcConfig)
	require.Error(t, err)
	// Should contain suggestion about supported hosts
	assert.Contains(t, err.Error(), "appservice")
}

func Test_ServiceManager_GetServiceTarget_HappyPath(t *testing.T) {
	env := environment.NewWithValues("test", map[string]string{})
	target := &fakeServiceTargetStub{}
	locator := &fakeServiceLocator{target: target}
	sm := makeServiceManager(env, locator, &fakeResourceManager{})

	svcConfig := makeSvcConfigWithDispatcher("web", ServiceLanguageJavaScript, AppServiceTarget, t.TempDir())

	result, err := sm.GetServiceTarget(t.Context(), svcConfig)
	require.NoError(t, err)
	assert.Same(t, target, result)
}

// Test_ServiceManager_GetRequiredTools_NoTools verifies the empty-tools branch
// where the framework and service target require no external tools.
func Test_ServiceManager_GetRequiredTools_NoTools(t *testing.T) {
	env := environment.NewWithValues("test", map[string]string{})
	framework := NewNoOpProject(env)
	target := &fakeServiceTargetStub{}
	locator := &fakeServiceLocator{framework: framework, target: target}
	sm := makeServiceManager(env, locator, &fakeResourceManager{})

	svcConfig := makeSvcConfigWithDispatcher("web", ServiceLanguageJavaScript, AppServiceTarget, t.TempDir())

	tools, err := sm.GetRequiredTools(t.Context(), svcConfig)
	require.NoError(t, err)
	assert.Empty(t, tools)
}

// ---------- appendOperationArtifacts: invalid event type ----------
func Test_appendOperationArtifacts_InvalidEvent(t *testing.T) {
	sc := NewServiceContext()
	err := appendOperationArtifacts(sc, ext.Event("invalid"), nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid operation phase")
}

func Test_appendOperationArtifacts_NilContext(t *testing.T) {
	err := appendOperationArtifacts(nil, ServiceEventPackage, nil)
	require.NoError(t, err)
}

func Test_appendOperationArtifacts_AllEvents(t *testing.T) {
	events := []ext.Event{
		ServiceEventRestore,
		ServiceEventBuild,
		ServiceEventPackage,
		ServiceEventPublish,
		ServiceEventDeploy,
	}
	for _, ev := range events {
		t.Run(string(ev), func(t *testing.T) {
			sc := NewServiceContext()
			err := appendOperationArtifacts(sc, ev, nil)
			require.NoError(t, err)
		})
	}
}

// ---------- isComponentInitialized: already-initialized path ----------
func Test_isComponentInitialized(t *testing.T) {
	env := environment.NewWithValues("test-env", map[string]string{})
	sm := &serviceManager{
		env:            env,
		operationCache: make(ServiceOperationCache),
		initialized:    make(map[*ServiceConfig]map[any]bool),
	}

	sc := &ServiceConfig{Name: "api"}
	fakeComponent := "framework-service"

	// First call: not initialized, creates empty map
	ok := sm.isComponentInitialized(sc, fakeComponent)
	assert.False(t, ok)

	// Mark as initialized
	sm.initialized[sc][fakeComponent] = true

	// Second call: is initialized
	ok = sm.isComponentInitialized(sc, fakeComponent)
	assert.True(t, ok)

	// Third call with different component: not initialized
	ok = sm.isComponentInitialized(sc, "other-component")
	assert.False(t, ok)
}

// ---------- serviceManager Initialize: framework init error, target init error, already initialized ----------
func Test_serviceManager_Initialize(t *testing.T) {
	t.Run("FrameworkInit_Error", func(t *testing.T) {
		env := environment.NewWithValues("test-env", map[string]string{})
		sm := &serviceManager{
			env:            env,
			operationCache: make(ServiceOperationCache),
			initialized:    make(map[*ServiceConfig]map[any]bool),
			serviceLocator: newFakeLocator(nil, nil),
		}

		sc := makeSvcConfig("api", "api", ContainerAppTarget, ServiceLanguagePython, t.TempDir())
		sc.Project.EventDispatcher = ext.NewEventDispatcher[ProjectLifecycleEventArgs]()

		err := sm.Initialize(t.Context(), sc)
		// Should get "getting framework service" error since Python is not registered
		require.Error(t, err)
	})

	t.Run("AlreadyInitialized_Skips", func(t *testing.T) {
		env := environment.NewWithValues("test-env", map[string]string{})
		fakeFramework := &noOpProject{}
		fakeTarget := &fakeConfigurableServiceTarget{}

		sm := &serviceManager{
			env:            env,
			operationCache: make(ServiceOperationCache),
			initialized:    make(map[*ServiceConfig]map[any]bool),
			serviceLocator: newFakeLocator(fakeFramework, fakeTarget),
		}

		sc := makeSvcConfig("api", "api", ContainerAppTarget, ServiceLanguageDocker, t.TempDir())
		sc.Project.EventDispatcher = ext.NewEventDispatcher[ProjectLifecycleEventArgs]()

		// First initialization
		err := sm.Initialize(t.Context(), sc)
		require.NoError(t, err)

		// Second initialization - should skip (already initialized)
		err = sm.Initialize(t.Context(), sc)
		require.NoError(t, err)
	})
}

// ---------- serviceManager.Restore: cache hit ----------
func Test_serviceManager_Restore_CacheHit(t *testing.T) {
	env := environment.NewWithValues("test-env", map[string]string{})
	fakeFramework := &noOpProject{}
	fakeTarget := &fakeConfigurableServiceTarget{}

	sm := &serviceManager{
		env:            env,
		operationCache: make(ServiceOperationCache),
		initialized:    make(map[*ServiceConfig]map[any]bool),
		serviceLocator: newFakeLocator(fakeFramework, fakeTarget),
	}

	sc := makeSvcConfig("api", "api", ContainerAppTarget, ServiceLanguageDocker, t.TempDir())
	sc.Project.EventDispatcher = ext.NewEventDispatcher[ProjectLifecycleEventArgs]()

	// Seed cache with a restore result
	cachedResult := &ServiceRestoreResult{}
	sm.setOperationResult(sc, ServiceEventRestore, cachedResult)

	result, err := sm.Restore(t.Context(), sc, nil, nil)
	require.NoError(t, err)
	assert.Equal(t, cachedResult, result)
}

// ---------- UnsupportedServiceHostError ----------
func Test_UnsupportedServiceHostError(t *testing.T) {
	err := &UnsupportedServiceHostError{
		Host:        "unknown-host",
		ServiceName: "api",
	}
	assert.Contains(t, err.Error(), "unknown-host")
	assert.Contains(t, err.Error(), "api")
}

func (f *fakeSimpleServiceLocator) ResolveNamed(name string, o any) error {
	switch ptr := o.(type) {
	case *FrameworkService:
		if f.framework != nil {
			*ptr = f.framework
			return nil
		}
		return &UnsupportedServiceHostError{Host: name}
	case *ServiceTarget:
		if f.target != nil {
			*ptr = f.target
			return nil
		}
		return &UnsupportedServiceHostError{Host: name}
	case *CompositeFrameworkService:
		return &UnsupportedServiceHostError{Host: name}
	}
	return nil
}

func (f *fakeSimpleServiceLocator) Resolve(_ any) error {
	return nil
}

func (f *fakeSimpleServiceLocator) Invoke(_ any) error {
	return nil
}
