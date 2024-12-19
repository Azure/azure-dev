package project

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
	"github.com/azure/azure-dev/cli/azd/pkg/alpha"
	"github.com/azure/azure-dev/cli/azd/pkg/async"
	"github.com/azure/azure-dev/cli/azd/pkg/azapi"
	"github.com/azure/azure-dev/cli/azd/pkg/azure"
	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/ext"
	"github.com/azure/azure-dev/cli/azd/pkg/infra"
	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockarmresources"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockazapi"
	"github.com/stretchr/testify/require"
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
	mockContext := mocks.NewMockContext(context.Background())
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
	mockContext := mocks.NewMockContext(context.Background())
	setupMocksForServiceManager(mockContext)
	env := environment.New("test")
	sm := createServiceManager(mockContext, env, ServiceOperationCache{})
	serviceConfig := createTestServiceConfig("./src/api", ServiceTargetFake, ServiceLanguageFake)

	err := sm.Initialize(*mockContext.Context, serviceConfig)
	require.NoError(t, err)
}

func Test_ServiceManager_Restore(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())
	setupMocksForServiceManager(mockContext)
	env := environment.New("test")
	sm := createServiceManager(mockContext, env, ServiceOperationCache{})
	serviceConfig := createTestServiceConfig("./src/api", ServiceTargetFake, ServiceLanguageFake)

	raisedPreRestoreEvent := false
	raisedPostRestoreEvent := false

	_ = serviceConfig.AddHandler("prerestore", func(ctx context.Context, args ServiceLifecycleEventArgs) error {
		raisedPreRestoreEvent = true
		return nil
	})

	_ = serviceConfig.AddHandler("postrestore", func(ctx context.Context, args ServiceLifecycleEventArgs) error {
		raisedPostRestoreEvent = true
		return nil
	})

	restoreCalled := to.Ptr(false)
	ctx := context.WithValue(*mockContext.Context, frameworkRestoreCalled, restoreCalled)
	result, err := logProgress(t, func(progess *async.Progress[ServiceProgress]) (*ServiceRestoreResult, error) {
		return sm.Restore(ctx, serviceConfig, progess)
	})

	require.NoError(t, err)
	require.NotNil(t, result)
	require.True(t, *restoreCalled)
	require.True(t, raisedPreRestoreEvent)
	require.True(t, raisedPostRestoreEvent)
}

func Test_ServiceManager_Build(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())
	setupMocksForServiceManager(mockContext)
	env := environment.New("test")
	sm := createServiceManager(mockContext, env, ServiceOperationCache{})
	serviceConfig := createTestServiceConfig("./src/api", ServiceTargetFake, ServiceLanguageFake)

	raisedPreBuildEvent := false
	raisedPostBuildEvent := false

	_ = serviceConfig.AddHandler("prebuild", func(ctx context.Context, args ServiceLifecycleEventArgs) error {
		raisedPreBuildEvent = true
		return nil
	})

	_ = serviceConfig.AddHandler("postbuild", func(ctx context.Context, args ServiceLifecycleEventArgs) error {
		raisedPostBuildEvent = true
		return nil
	})

	buildCalled := to.Ptr(false)
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
	mockContext := mocks.NewMockContext(context.Background())
	setupMocksForServiceManager(mockContext)
	env := environment.New("test")
	sm := createServiceManager(mockContext, env, ServiceOperationCache{})
	serviceConfig := createTestServiceConfig("./src/api", ServiceTargetFake, ServiceLanguageFake)

	raisedPrePackageEvent := false
	raisedPostPackageEvent := false

	_ = serviceConfig.AddHandler("prepackage", func(ctx context.Context, args ServiceLifecycleEventArgs) error {
		raisedPrePackageEvent = true
		return nil
	})

	_ = serviceConfig.AddHandler("postpackage", func(ctx context.Context, args ServiceLifecycleEventArgs) error {
		raisedPostPackageEvent = true
		return nil
	})

	fakeFrameworkPackageCalled := to.Ptr(false)
	fakeServiceTargetPackageCalled := to.Ptr(false)
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
	mockContext := mocks.NewMockContext(context.Background())
	setupMocksForServiceManager(mockContext)
	env := environment.NewWithValues("test", map[string]string{
		environment.SubscriptionIdEnvVarName: "SUBSCRIPTION_ID",
	})
	sm := createServiceManager(mockContext, env, ServiceOperationCache{})
	serviceConfig := createTestServiceConfig("./src/api", ServiceTargetFake, ServiceLanguageFake)

	raisedPreDeployEvent := false
	raisedPostDeployEvent := false

	_ = serviceConfig.AddHandler("predeploy", func(ctx context.Context, args ServiceLifecycleEventArgs) error {
		raisedPreDeployEvent = true
		return nil
	})

	_ = serviceConfig.AddHandler("postdeploy", func(ctx context.Context, args ServiceLifecycleEventArgs) error {
		raisedPostDeployEvent = true
		return nil
	})

	deployCalled := to.Ptr(false)
	ctx := context.WithValue(*mockContext.Context, serviceTargetDeployCalled, deployCalled)

	result, err := logProgress(t, func(progess *async.Progress[ServiceProgress]) (*ServiceDeployResult, error) {
		return sm.Deploy(ctx, serviceConfig, nil, progess)
	})

	require.NoError(t, err)
	require.NotNil(t, result)
	require.True(t, *deployCalled)
	require.True(t, raisedPreDeployEvent)
	require.True(t, raisedPostDeployEvent)
}

func Test_ServiceManager_GetFrameworkService(t *testing.T) {
	t.Run("Standard", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
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
		mockContext := mocks.NewMockContext(context.Background())
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
		mockContext := mocks.NewMockContext(context.Background())
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
	mockContext := mocks.NewMockContext(context.Background())
	setupMocksForServiceManager(mockContext)
	env := environment.New("test")
	sm := createServiceManager(mockContext, env, ServiceOperationCache{})
	serviceConfig := createTestServiceConfig("./src/api", ServiceTargetFake, ServiceLanguageFake)

	serviceTarget, err := sm.GetServiceTarget(*mockContext.Context, serviceConfig)
	require.NoError(t, err)
	require.NotNil(t, serviceTarget)
	require.IsType(t, new(fakeServiceTarget), serviceTarget)
}

func Test_ServiceManager_CacheResults(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())
	setupMocksForServiceManager(mockContext)
	env := environment.New("test")
	sm := createServiceManager(mockContext, env, ServiceOperationCache{})
	serviceConfig := createTestServiceConfig("./src/api", ServiceTargetFake, ServiceLanguageFake)

	buildCalled := to.Ptr(false)
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
	mockContext := mocks.NewMockContext(context.Background())
	setupMocksForServiceManager(mockContext)
	env := environment.New("test")

	operationCache := ServiceOperationCache{}

	sm1 := createServiceManager(mockContext, env, operationCache)
	serviceConfig := createTestServiceConfig("./src/api", ServiceTargetFake, ServiceLanguageFake)

	packageCalled := to.Ptr(false)
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
						return serviceManager.Restore(ctx, serviceConfig, progess)
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
						return serviceManager.Deploy(ctx, serviceConfig, nil, progress)
					})
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			mockContext := mocks.NewMockContext(context.Background())
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
			ID:       to.Ptr("ID"),
			Name:     to.Ptr("RESOURCE_GROUP"),
			Location: to.Ptr("eastus2"),
			Type:     to.Ptr(string(azapi.AzureResourceTypeResourceGroup)),
		},
	})

	mockarmresources.AddAzResourceListMock(
		mockContext.HttpClient,
		to.Ptr("RESOURCE_GROUP"),
		[]*armresources.GenericResourceExpanded{
			{
				ID:       to.Ptr("ID"),
				Name:     to.Ptr("WEB_APP"),
				Location: to.Ptr("eastus2"),
				Type:     to.Ptr(string(azapi.AzureResourceTypeWebSite)),
				Tags: map[string]*string{
					azure.TagKeyAzdServiceName: to.Ptr("api"),
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
		Details: result,
	}, nil
}

func (f *fakeFramework) Build(
	ctx context.Context,
	serviceConfig *ServiceConfig,
	restoreOutput *ServiceRestoreResult,
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
		Restore: restoreOutput,
		Details: result,
	}, nil
}

func (f *fakeFramework) Package(
	ctx context.Context,
	serviceConfig *ServiceConfig,
	buildOutput *ServiceBuildResult,
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
		Build:   buildOutput,
		Details: result,
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
	packageOutput *ServicePackageResult,
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
		Build:   packageOutput.Build,
		Details: result,
	}, nil
}

func (st *fakeServiceTarget) Deploy(
	ctx context.Context,
	serviceConfig *ServiceConfig,
	packageOutput *ServicePackageResult,
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
		Package: packageOutput,
		Details: result,
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
