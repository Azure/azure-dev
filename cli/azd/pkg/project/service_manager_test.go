package project

import (
	"context"
	"errors"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
	"github.com/azure/azure-dev/cli/azd/pkg/alpha"
	"github.com/azure/azure-dev/cli/azd/pkg/async"
	"github.com/azure/azure-dev/cli/azd/pkg/azure"
	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/azure/azure-dev/cli/azd/pkg/convert"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/ext"
	"github.com/azure/azure-dev/cli/azd/pkg/infra"
	"github.com/azure/azure-dev/cli/azd/pkg/ioc"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockarmresources"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockazcli"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

const (
	ServiceLanguageFake ServiceLanguageKind = "fake-framework"
	ServiceTargetFake   ServiceTargetKind   = "fake-service-target"
)

func createServiceManager(
	mockContext *mocks.MockContext,
	env *environment.Environment,
	operationCache ServiceOperationCache,
) ServiceManager {
	azCli := mockazcli.NewAzCliFromMockContext(mockContext)
	depOpService := mockazcli.NewDeploymentOperationsServiceFromMockContext(mockContext)
	resourceManager := NewResourceManager(env, azCli, depOpService)

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

	fakeServiceTarget := &fakeServiceTarget{}
	fakeServiceTarget.
		On("RequiredExternalTools", *mockContext.Context).
		Return([]tools.ExternalTool{&fakeTool{}})

	fakeFramework := &fakeFramework{}
	fakeFramework.
		On("RequiredExternalTools", *mockContext.Context).
		Return([]tools.ExternalTool{&fakeTool{}})

	setupMocksForServiceManager(mockContext, fakeServiceTarget, fakeFramework)
	env := environment.New("test")
	serviceManager := createServiceManager(mockContext, env, ServiceOperationCache{})
	serviceConfig := createTestServiceConfig("./src/api", ServiceTargetFake, ServiceLanguageFake)
	tools, err := serviceManager.GetRequiredTools(*mockContext.Context, serviceConfig)
	require.NoError(t, err)
	// Both require a tool, but only 1 unique tool
	require.Len(t, tools, 1)

	fakeServiceTarget.AssertCalled(t, "RequiredExternalTools", *mockContext.Context)
	fakeFramework.AssertCalled(t, "RequiredExternalTools", *mockContext.Context)
}

func Test_ServiceManager_Initialize(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())

	env := environment.New("test")
	serviceManager := createServiceManager(mockContext, env, ServiceOperationCache{})
	serviceConfig := createTestServiceConfig("./src/api", ServiceTargetFake, ServiceLanguageFake)

	fakeServiceTarget := &fakeServiceTarget{}
	fakeServiceTarget.On("Initialize", *mockContext.Context, serviceConfig).Return(nil)

	fakeFramework := &fakeFramework{}
	fakeFramework.
		On("Initialize", *mockContext.Context, serviceConfig.ComponentConfig).
		Return(nil)

	setupMocksForServiceManager(mockContext, fakeServiceTarget, fakeFramework)

	err := serviceManager.Initialize(*mockContext.Context, serviceConfig)
	require.NoError(t, err)

	fakeServiceTarget.AssertCalled(t, "Initialize", *mockContext.Context, serviceConfig)
	fakeFramework.AssertCalled(t, "Initialize", *mockContext.Context, serviceConfig.ComponentConfig)
}

func Test_ServiceManager_Restore(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())

	env := environment.New("test")

	raisedPreRestoreEvent := false
	raisedPostRestoreEvent := false
	serviceConfig := createTestServiceConfig("./src/api", ServiceTargetFake, ServiceLanguageFake)

	_ = serviceConfig.AddHandler("prerestore", func(ctx context.Context, args ServiceLifecycleEventArgs) error {
		raisedPreRestoreEvent = true
		return nil
	})

	_ = serviceConfig.AddHandler("postrestore", func(ctx context.Context, args ServiceLifecycleEventArgs) error {
		raisedPostRestoreEvent = true
		return nil
	})

	fakeServiceTarget := newFakeServiceTarget()

	fakeFramework := &fakeFramework{}
	frameworkRestoreTask := async.RunTaskWithProgress(
		func(task *async.TaskContextWithProgress[*ServiceRestoreResult, ServiceProgress]) {
			task.SetProgress(NewServiceProgress("Restoring"))
			task.SetResult(&ServiceRestoreResult{})
		},
	)

	fakeFramework.
		On("Restore", *mockContext.Context, serviceConfig.ComponentConfig).
		Return(frameworkRestoreTask)

	setupMocksForServiceManager(mockContext, fakeServiceTarget, fakeFramework)

	serviceManager := createServiceManager(mockContext, env, ServiceOperationCache{})
	restoreTask := serviceManager.Restore(*mockContext.Context, serviceConfig)
	logProgress(restoreTask)

	result, err := restoreTask.Await()
	require.NoError(t, err)
	require.NotNil(t, result)
	require.True(t, raisedPreRestoreEvent)
	require.True(t, raisedPostRestoreEvent)

	fakeFramework.AssertCalled(t, "Restore", *mockContext.Context, serviceConfig.ComponentConfig)
}

func Test_ServiceManager_Build(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())
	env := environment.New("test")

	raisedPreBuildEvent := false
	raisedPostBuildEvent := false
	serviceConfig := createTestServiceConfig("./src/api", ServiceTargetFake, ServiceLanguageFake)

	_ = serviceConfig.AddHandler("prebuild", func(ctx context.Context, args ServiceLifecycleEventArgs) error {
		raisedPreBuildEvent = true
		return nil
	})

	_ = serviceConfig.AddHandler("postbuild", func(ctx context.Context, args ServiceLifecycleEventArgs) error {
		raisedPostBuildEvent = true
		return nil
	})

	fakeServiceTarget := &fakeServiceTarget{}

	frameworkBuildTask := async.RunTaskWithProgress(
		func(task *async.TaskContextWithProgress[*ServiceBuildResult, ServiceProgress]) {
			task.SetProgress(NewServiceProgress("Building"))
			task.SetResult(&ServiceBuildResult{})
		},
	)

	var restoreResult *ServiceRestoreResult
	fakeFramework := &fakeFramework{}
	fakeFramework.
		On("Build", *mockContext.Context, serviceConfig.ComponentConfig, restoreResult).
		Return(frameworkBuildTask)

	setupMocksForServiceManager(mockContext, fakeServiceTarget, fakeFramework)

	serviceManager := createServiceManager(mockContext, env, ServiceOperationCache{})
	buildTask := serviceManager.Build(*mockContext.Context, serviceConfig, restoreResult)
	logProgress(buildTask)

	result, err := buildTask.Await()
	require.NoError(t, err)
	require.NotNil(t, result)
	require.True(t, raisedPreBuildEvent)
	require.True(t, raisedPostBuildEvent)

	fakeFramework.AssertCalled(t, "Build", *mockContext.Context, serviceConfig.ComponentConfig, restoreResult)
}

func Test_ServiceManager_Package(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())
	env := environment.New("test")

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

	serviceTargetPackageTask := async.RunTaskWithProgress(
		func(task *async.TaskContextWithProgress[*ServicePackageResult, ServiceProgress]) {
			task.SetProgress(NewServiceProgress("Packaging"))
			task.SetResult(&ServicePackageResult{})
		},
	)

	fakeServiceTarget := &fakeServiceTarget{}
	fakeServiceTarget.
		On("Package", *mockContext.Context, serviceConfig, mock.Anything).
		Return(serviceTargetPackageTask)

	frameworkPackageTask := async.RunTaskWithProgress(
		func(ctx *async.TaskContextWithProgress[*ServicePackageResult, ServiceProgress]) {
			ctx.SetProgress(NewServiceProgress("Packaging"))
			ctx.SetResult(&ServicePackageResult{})
		},
	)

	fakeFramework := &fakeFramework{}
	fakeFramework.On("Requirements").Return(FrameworkRequirements{
		Package: FrameworkPackageRequirements{
			RequireRestore: false,
			RequireBuild:   false,
		},
	})

	fakeFramework.
		On("Package", *mockContext.Context, serviceConfig.ComponentConfig, mock.Anything).
		Return(frameworkPackageTask)

	setupMocksForServiceManager(mockContext, fakeServiceTarget, fakeFramework)
	serviceManager := createServiceManager(mockContext, env, ServiceOperationCache{})
	packageTask := serviceManager.Package(*mockContext.Context, serviceConfig, nil, nil)
	logProgress(packageTask)

	result, err := packageTask.Await()
	require.NoError(t, err)
	require.NotNil(t, result)
	require.True(t, raisedPrePackageEvent)
	require.True(t, raisedPostPackageEvent)

	fakeServiceTarget.AssertCalled(t, "Package", *mockContext.Context, serviceConfig, mock.Anything)
	fakeFramework.AssertCalled(t, "Package", *mockContext.Context, serviceConfig.ComponentConfig, mock.Anything)
}

func Test_ServiceManager_Deploy(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())
	env := environment.NewWithValues("test", map[string]string{
		environment.SubscriptionIdEnvVarName: "SUBSCRIPTION_ID",
	})
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

	serviceTargetDeployTask := async.RunTaskWithProgress(
		func(ctx *async.TaskContextWithProgress[*ServiceDeployResult, ServiceProgress]) {
			ctx.SetProgress(NewServiceProgress("Deploying"))
			ctx.SetResult(&ServiceDeployResult{})
		},
	)

	fakeServiceTarget := &fakeServiceTarget{}
	fakeServiceTarget.
		On("Deploy", *mockContext.Context, serviceConfig, mock.Anything, mock.Anything).
		Return(serviceTargetDeployTask)

	fakeFramework := &fakeFramework{}

	setupMocksForServiceManager(mockContext, fakeServiceTarget, fakeFramework)

	serviceManager := createServiceManager(mockContext, env, ServiceOperationCache{})
	deployTask := serviceManager.Deploy(*mockContext.Context, serviceConfig, nil)
	logProgress(deployTask)

	result, err := deployTask.Await()
	require.NoError(t, err)
	require.NotNil(t, result)
	require.True(t, raisedPreDeployEvent)
	require.True(t, raisedPostDeployEvent)

	fakeServiceTarget.AssertCalled(t, "Deploy", *mockContext.Context, serviceConfig, mock.Anything, mock.Anything)
}

func Test_ServiceManager_GetFrameworkService(t *testing.T) {
	t.Run("Standard", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		setupMocksForServiceManager(mockContext, newFakeServiceTarget(), newFakeFramework())
		env := environment.New("test")
		sm := createServiceManager(mockContext, env, ServiceOperationCache{})
		serviceConfig := createTestServiceConfig("./src/api", ServiceTargetFake, ServiceLanguageFake)

		framework, err := sm.GetFrameworkService(*mockContext.Context, serviceConfig.ComponentConfig)
		require.NoError(t, err)
		require.NotNil(t, framework)
		require.IsType(t, new(fakeFramework), framework)
	})

	t.Run("No project path and has docker tag", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		mockContext.Container.MustRegisterNamedTransient("docker", newFakeFramework)

		setupMocksForServiceManager(mockContext, newFakeServiceTarget(), newFakeFramework())
		env := environment.New("test")
		serviceManager := createServiceManager(mockContext, env, ServiceOperationCache{})
		serviceConfig := createTestServiceConfig("", ServiceTargetFake, ServiceLanguageNone)
		serviceConfig.Image = "nginx"

		framework, err := serviceManager.GetFrameworkService(*mockContext.Context, serviceConfig.ComponentConfig)
		require.NoError(t, err)
		require.NotNil(t, framework)
		require.IsType(t, new(fakeFramework), framework)
	})

	t.Run("No project path or docker tag", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		mockContext.Container.MustRegisterNamedTransient("docker", newFakeFramework)

		setupMocksForServiceManager(mockContext, newFakeServiceTarget(), newFakeFramework())
		env := environment.New("test")
		serviceManager := createServiceManager(mockContext, env, ServiceOperationCache{})
		serviceConfig := createTestServiceConfig("", ServiceTargetFake, ServiceLanguageNone)

		require.Panics(t, func() {
			_, _ = serviceManager.GetFrameworkService(*mockContext.Context, serviceConfig.ComponentConfig)
		})
	})
}

func Test_ServiceManager_GetServiceTarget(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())
	setupMocksForServiceManager(mockContext, newFakeServiceTarget(), newFakeFramework())
	env := environment.New("test")
	serviceManager := createServiceManager(mockContext, env, ServiceOperationCache{})
	serviceConfig := createTestServiceConfig("./src/api", ServiceTargetFake, ServiceLanguageFake)

	serviceTarget, err := serviceManager.GetServiceTarget(*mockContext.Context, serviceConfig)
	require.NoError(t, err)
	require.NotNil(t, serviceTarget)
	require.IsType(t, new(fakeServiceTarget), serviceTarget)
}

func Test_ServiceManager_CacheResults(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())

	env := environment.New("test")
	serviceConfig := createTestServiceConfig("./src/api", ServiceTargetFake, ServiceLanguageFake)

	fakeServiceTarget := &fakeServiceTarget{}

	frameworkBuildTask := async.RunTaskWithProgress(
		func(task *async.TaskContextWithProgress[*ServiceBuildResult, ServiceProgress]) {
			task.SetProgress(NewServiceProgress("Building"))
			task.SetResult(&ServiceBuildResult{})
		},
	)

	fakeFramework := &fakeFramework{}
	fakeFramework.
		On("Build", *mockContext.Context, serviceConfig.ComponentConfig, mock.Anything).
		Return(frameworkBuildTask)

	setupMocksForServiceManager(mockContext, fakeServiceTarget, fakeFramework)

	serviceManager := createServiceManager(mockContext, env, ServiceOperationCache{})
	buildTask1 := serviceManager.Build(*mockContext.Context, serviceConfig, nil)
	logProgress(buildTask1)

	buildResult1, _ := buildTask1.Await()

	buildTask2 := serviceManager.Build(*mockContext.Context, serviceConfig, nil)
	logProgress(buildTask1)

	buildResult2, _ := buildTask2.Await()

	require.Same(t, buildResult1, buildResult2)
	fakeFramework.AssertNumberOfCalls(t, "Build", 1)
}

func Test_ServiceManager_CacheResults_Across_Instances(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())
	env := environment.New("test")
	serviceConfig := createTestServiceConfig("./src/api", ServiceTargetFake, ServiceLanguageFake)

	serviceTargetPackageTask := async.RunTaskWithProgress(
		func(ctx *async.TaskContextWithProgress[*ServicePackageResult, ServiceProgress]) {
			ctx.SetProgress(NewServiceProgress("Packaging"))
			ctx.SetResult(&ServicePackageResult{})
		},
	)

	fakeServiceTarget := &fakeServiceTarget{}
	fakeServiceTarget.
		On("Package", *mockContext.Context, serviceConfig, mock.Anything).
		Return(serviceTargetPackageTask)

	frameworkPackageTask := async.RunTaskWithProgress(
		func(ctx *async.TaskContextWithProgress[*ServicePackageResult, ServiceProgress]) {
			ctx.SetProgress(NewServiceProgress("Packaging"))
			ctx.SetResult(&ServicePackageResult{})
		},
	)

	fakeFramework := &fakeFramework{}
	fakeFramework.On("Requirements").Return(FrameworkRequirements{
		Package: FrameworkPackageRequirements{
			RequireRestore: false,
			RequireBuild:   false,
		},
	})

	fakeFramework.
		On("Package", *mockContext.Context, serviceConfig.ComponentConfig, mock.Anything).
		Return(frameworkPackageTask)

	setupMocksForServiceManager(mockContext, fakeServiceTarget, fakeFramework)

	operationCache := ServiceOperationCache{}

	sm1 := createServiceManager(mockContext, env, operationCache)

	packageTask1 := sm1.Package(*mockContext.Context, serviceConfig, nil, nil)
	logProgress(packageTask1)

	packageResult1, _ := packageTask1.Await()

	sm2 := createServiceManager(mockContext, env, operationCache)
	packageTask2 := sm2.Package(*mockContext.Context, serviceConfig, nil, nil)
	logProgress(packageTask2)

	packageResult2, _ := packageTask2.Await()

	require.Same(t, packageResult1, packageResult2)

	fakeFramework.AssertNumberOfCalls(t, "Package", 1)
	fakeServiceTarget.AssertNumberOfCalls(t, "Package", 1)
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
				restoreTask := serviceManager.Restore(ctx, serviceConfig)
				logProgress(restoreTask)
				return restoreTask.Await()
			},
		},
		{
			name: "build",
			run: func(ctx context.Context, serviceManager ServiceManager, serviceConfig *ServiceConfig) (any, error) {
				buildTask := serviceManager.Build(ctx, serviceConfig, nil)
				logProgress(buildTask)
				return buildTask.Await()
			},
		},
		{
			name: "package",
			run: func(ctx context.Context, serviceManager ServiceManager, serviceConfig *ServiceConfig) (any, error) {
				packageTask := serviceManager.Package(ctx, serviceConfig, nil, nil)
				logProgress(packageTask)
				return packageTask.Await()
			},
		},
		{
			name: "deploy",
			run: func(ctx context.Context, serviceManager ServiceManager, serviceConfig *ServiceConfig) (any, error) {
				deployTask := serviceManager.Deploy(ctx, serviceConfig, nil)
				logProgress(deployTask)
				return deployTask.Await()
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			mockContext := mocks.NewMockContext(context.Background())
			setupMocksForServiceManager(mockContext, nil, nil)
			env := environment.NewWithValues("test", map[string]string{
				environment.SubscriptionIdEnvVarName: "SUBSCRIPTION_ID",
			})
			serviceManager := createServiceManager(mockContext, env, ServiceOperationCache{})
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

					result, err := test.run(*mockContext.Context, serviceManager, serviceConfig)
					require.Error(t, err)
					require.Nil(t, result)
				})
			}
		})
	}
}

func setupMocksForServiceManager(
	mockContext *mocks.MockContext,
	serviceTarget ServiceTarget,
	frameworkService FrameworkService,
) {
	ioc.RegisterNamedInstance(mockContext.Container, string(ServiceLanguageFake), frameworkService)
	ioc.RegisterNamedInstance(mockContext.Container, string(ServiceTargetFake), serviceTarget)

	mockarmresources.AddResourceGroupListMock(mockContext.HttpClient, "SUBSCRIPTION_ID", []*armresources.ResourceGroup{
		{
			ID:       convert.RefOf("ID"),
			Name:     convert.RefOf("RESOURCE_GROUP"),
			Location: convert.RefOf("eastus2"),
			Type:     convert.RefOf(string(infra.AzureResourceTypeResourceGroup)),
		},
	})

	mockarmresources.AddAzResourceListMock(
		mockContext.HttpClient,
		convert.RefOf("RESOURCE_GROUP"),
		[]*armresources.GenericResourceExpanded{
			{
				ID:       convert.RefOf("ID"),
				Name:     convert.RefOf("WEB_APP"),
				Location: convert.RefOf("eastus2"),
				Type:     convert.RefOf(string(infra.AzureResourceTypeWebSite)),
				Tags: map[string]*string{
					azure.TagKeyAzdServiceName: convert.RefOf("api"),
				},
			},
		},
	)
}

// Fake implementation of framework service
type fakeFramework struct {
	mock.Mock
}

func newFakeFramework() FrameworkService {
	return &fakeFramework{}
}

func (f *fakeFramework) Requirements() FrameworkRequirements {
	args := f.Called()
	return args.Get(0).(FrameworkRequirements)
}

func (f *fakeFramework) RequiredExternalTools(ctx context.Context) []tools.ExternalTool {
	args := f.Called(ctx)
	return args.Get(0).([]tools.ExternalTool)
}

func (f *fakeFramework) Initialize(ctx context.Context, component *ComponentConfig) error {
	args := f.Called(ctx, component)
	return args.Error(0)
}

func (f *fakeFramework) Restore(
	ctx context.Context,
	component *ComponentConfig,
) *async.TaskWithProgress[*ServiceRestoreResult, ServiceProgress] {
	args := f.Called(ctx, component)
	return args.Get(0).(*async.TaskWithProgress[*ServiceRestoreResult, ServiceProgress])
}

func (f *fakeFramework) Build(
	ctx context.Context,
	component *ComponentConfig,
	restoreOutput *ServiceRestoreResult,
) *async.TaskWithProgress[*ServiceBuildResult, ServiceProgress] {
	args := f.Called(ctx, component, restoreOutput)
	return args.Get(0).(*async.TaskWithProgress[*ServiceBuildResult, ServiceProgress])
}

func (f *fakeFramework) Package(
	ctx context.Context,
	component *ComponentConfig,
	buildOutput *ServiceBuildResult,
) *async.TaskWithProgress[*ServicePackageResult, ServiceProgress] {
	args := f.Called(ctx, component, buildOutput)
	return args.Get(0).(*async.TaskWithProgress[*ServicePackageResult, ServiceProgress])
}

// Fake implementation of a service target
type fakeServiceTarget struct {
	mock.Mock
}

func newFakeServiceTarget() ServiceTarget {
	return &fakeServiceTarget{}
}

func (st *fakeServiceTarget) Initialize(ctx context.Context, serviceConfig *ServiceConfig) error {
	args := st.Called(ctx, serviceConfig)
	return args.Error(0)
}

func (st *fakeServiceTarget) RequiredExternalTools(ctx context.Context) []tools.ExternalTool {
	args := st.Called(ctx)
	return args.Get(0).([]tools.ExternalTool)
}

func (st *fakeServiceTarget) Package(
	ctx context.Context,
	serviceConfig *ServiceConfig,
	packageOutput *ServicePackageResult,
) *async.TaskWithProgress[*ServicePackageResult, ServiceProgress] {
	args := st.Called(ctx, serviceConfig, packageOutput)
	return args.Get(0).(*async.TaskWithProgress[*ServicePackageResult, ServiceProgress])
}

func (st *fakeServiceTarget) Deploy(
	ctx context.Context,
	serviceConfig *ServiceConfig,
	packageOutput *ServicePackageResult,
	targetResource *environment.TargetResource,
) *async.TaskWithProgress[*ServiceDeployResult, ServiceProgress] {
	args := st.Called(ctx, serviceConfig, packageOutput, targetResource)
	return args.Get(0).(*async.TaskWithProgress[*ServiceDeployResult, ServiceProgress])
}

func (st *fakeServiceTarget) Endpoints(
	ctx context.Context,
	serviceConfig *ServiceConfig,
	targetResource *environment.TargetResource,
) ([]string, error) {
	args := st.Called(ctx, serviceConfig, targetResource)
	return args.Get(0).([]string), args.Error(1)
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
