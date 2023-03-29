package project

import (
	"context"
	"strings"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
	"github.com/azure/azure-dev/cli/azd/pkg/async"
	"github.com/azure/azure-dev/cli/azd/pkg/convert"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/infra"
	"github.com/azure/azure-dev/cli/azd/pkg/ioc"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockarmresources"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockazcli"
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
	serviceTargetPublishCalled contextKey = "serviceTargetPublishCalled"
)

func createServiceManager(mockContext *mocks.MockContext, env *environment.Environment) ServiceManager {
	azCli := mockazcli.NewAzCliFromMockContext(mockContext)
	resourceManager := NewResourceManager(env, azCli)
	serviceLocator := ioc.NewServiceLocator(mockContext.Container)

	return NewServiceManager(env, resourceManager, serviceLocator)
}

func Test_ServiceManager_GetRequiredTools(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())
	setupMocksForServiceManager(mockContext)
	env := environment.Ephemeral()
	sm := createServiceManager(mockContext, env)
	serviceConfig := createTestServiceConfig("./src/api", ServiceTargetFake, ServiceLanguageFake)

	tools, err := sm.GetRequiredTools(*mockContext.Context, serviceConfig)
	require.NoError(t, err)
	// Both require a tool, but only 1 unique tool
	require.Len(t, tools, 1)
}

func Test_ServiceManager_Initialize(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())
	setupMocksForServiceManager(mockContext)
	env := environment.Ephemeral()
	sm := createServiceManager(mockContext, env)
	serviceConfig := createTestServiceConfig("./src/api", ServiceTargetFake, ServiceLanguageFake)

	err := sm.Initialize(*mockContext.Context, serviceConfig)
	require.NoError(t, err)
}

func Test_ServiceManager_Restore(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())
	setupMocksForServiceManager(mockContext)
	env := environment.Ephemeral()
	sm := createServiceManager(mockContext, env)
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

	restoreCalled := convert.RefOf(false)
	ctx := context.WithValue(*mockContext.Context, frameworkRestoreCalled, restoreCalled)

	restoreTask := sm.Restore(ctx, serviceConfig)
	logProgress(restoreTask)

	result, err := restoreTask.Await()
	require.NoError(t, err)
	require.NotNil(t, result)
	require.True(t, *restoreCalled)
	require.True(t, raisedPreRestoreEvent)
	require.True(t, raisedPostRestoreEvent)
}

func Test_ServiceManager_Build(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())
	setupMocksForServiceManager(mockContext)
	env := environment.Ephemeral()
	sm := createServiceManager(mockContext, env)
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

	buildCalled := convert.RefOf(false)
	ctx := context.WithValue(*mockContext.Context, frameworkBuildCalled, buildCalled)

	buildTask := sm.Build(ctx, serviceConfig, nil)
	logProgress(buildTask)

	result, err := buildTask.Await()
	require.NoError(t, err)
	require.NotNil(t, result)
	require.True(t, *buildCalled)
	require.True(t, raisedPreBuildEvent)
	require.True(t, raisedPostBuildEvent)
}

func Test_ServiceManager_Package(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())
	setupMocksForServiceManager(mockContext)
	env := environment.Ephemeral()
	sm := createServiceManager(mockContext, env)
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

	fakeFrameworkPackageCalled := convert.RefOf(false)
	fakeServiceTargetPackageCalled := convert.RefOf(false)
	ctx := context.WithValue(*mockContext.Context, frameworkPackageCalled, fakeFrameworkPackageCalled)
	ctx = context.WithValue(ctx, serviceTargetPackageCalled, fakeServiceTargetPackageCalled)

	packageTask := sm.Package(ctx, serviceConfig, nil)
	logProgress(packageTask)

	result, err := packageTask.Await()
	require.NoError(t, err)
	require.NotNil(t, result)
	require.True(t, *fakeFrameworkPackageCalled)
	require.True(t, *fakeServiceTargetPackageCalled)
	require.True(t, raisedPrePackageEvent)
	require.True(t, raisedPostPackageEvent)
}

func Test_ServiceManager_Publish(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())
	setupMocksForServiceManager(mockContext)
	env := environment.EphemeralWithValues("test", map[string]string{
		environment.SubscriptionIdEnvVarName: "SUBSCRIPTION_ID",
	})
	sm := createServiceManager(mockContext, env)
	serviceConfig := createTestServiceConfig("./src/api", ServiceTargetFake, ServiceLanguageFake)

	raisedPrePublishEvent := false
	raisedPostPublishEvent := false

	_ = serviceConfig.AddHandler("prepublish", func(ctx context.Context, args ServiceLifecycleEventArgs) error {
		raisedPrePublishEvent = true
		return nil
	})

	_ = serviceConfig.AddHandler("postpublish", func(ctx context.Context, args ServiceLifecycleEventArgs) error {
		raisedPostPublishEvent = true
		return nil
	})

	publishCalled := convert.RefOf(false)
	ctx := context.WithValue(*mockContext.Context, serviceTargetPublishCalled, publishCalled)

	publishTask := sm.Publish(ctx, serviceConfig, nil)
	logProgress(publishTask)

	result, err := publishTask.Await()
	require.NoError(t, err)
	require.NotNil(t, result)
	require.True(t, *publishCalled)
	require.True(t, raisedPrePublishEvent)
	require.True(t, raisedPostPublishEvent)
}

func Test_ServiceManager_Deploy(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())
	setupMocksForServiceManager(mockContext)
	env := environment.EphemeralWithValues("test", map[string]string{
		environment.SubscriptionIdEnvVarName: "SUBSCRIPTION_ID",
	})
	sm := createServiceManager(mockContext, env)
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

	restoreCalled := convert.RefOf(false)
	buildCalled := convert.RefOf(false)
	fakeFrameworkPackageCalled := convert.RefOf(false)
	fakeServiceTargetPackageCalled := convert.RefOf(false)
	publishCalled := convert.RefOf(false)
	ctx := context.WithValue(*mockContext.Context, frameworkRestoreCalled, restoreCalled)
	ctx = context.WithValue(ctx, frameworkBuildCalled, buildCalled)
	ctx = context.WithValue(ctx, frameworkPackageCalled, fakeFrameworkPackageCalled)
	ctx = context.WithValue(ctx, serviceTargetPackageCalled, fakeServiceTargetPackageCalled)
	ctx = context.WithValue(ctx, serviceTargetPublishCalled, publishCalled)

	deployTask := sm.Deploy(ctx, serviceConfig)
	logProgress(deployTask)

	result, err := deployTask.Await()
	require.NoError(t, err)
	require.NotNil(t, result)
	require.True(t, *restoreCalled)
	require.True(t, *buildCalled)
	require.True(t, *fakeFrameworkPackageCalled)
	require.True(t, *fakeServiceTargetPackageCalled)
	require.True(t, raisedPreDeployEvent)
	require.True(t, raisedPostDeployEvent)
}

func Test_ServiceManager_GetFrameworkService(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())
	setupMocksForServiceManager(mockContext)
	env := environment.Ephemeral()
	sm := createServiceManager(mockContext, env)
	serviceConfig := createTestServiceConfig("./src/api", ServiceTargetFake, ServiceLanguageFake)

	framework, err := sm.GetFrameworkService(*mockContext.Context, serviceConfig)
	require.NoError(t, err)
	require.NotNil(t, framework)
	require.IsType(t, new(fakeFramework), framework)
}

func Test_ServiceManager_GetServiceTarget(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())
	setupMocksForServiceManager(mockContext)
	env := environment.Ephemeral()
	sm := createServiceManager(mockContext, env)
	serviceConfig := createTestServiceConfig("./src/api", ServiceTargetFake, ServiceLanguageFake)

	serviceTarget, err := sm.GetServiceTarget(*mockContext.Context, serviceConfig)
	require.NoError(t, err)
	require.NotNil(t, serviceTarget)
	require.IsType(t, new(fakeServiceTarget), serviceTarget)
}

func Test_ServiceManager_CacheResults(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())
	setupMocksForServiceManager(mockContext)
	env := environment.Ephemeral()
	sm := createServiceManager(mockContext, env)
	serviceConfig := createTestServiceConfig("./src/api", ServiceTargetFake, ServiceLanguageFake)

	buildCalled := convert.RefOf(false)
	ctx := context.WithValue(*mockContext.Context, frameworkBuildCalled, buildCalled)

	buildTask1 := sm.Build(ctx, serviceConfig, nil)
	logProgress(buildTask1)

	buildResult1, _ := buildTask1.Await()

	require.True(t, *buildCalled)
	*buildCalled = false

	buildTask2 := sm.Build(ctx, serviceConfig, nil)
	logProgress(buildTask1)

	buildResult2, _ := buildTask2.Await()

	require.False(t, *buildCalled)
	require.Same(t, buildResult1, buildResult2)
}

func setupMocksForServiceManager(mockContext *mocks.MockContext) {
	_ = mockContext.Container.RegisterNamedSingleton(string(ServiceLanguageFake), newFakeFramework)
	_ = mockContext.Container.RegisterNamedSingleton(string(ServiceTargetFake), newFakeServiceTarget)

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
		return strings.Contains(command, "fake-service-target publish")
	}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
		return exec.NewRunResult(0, "", ""), nil
	})

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
					"azd-service-name": convert.RefOf("api"),
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

func (f *fakeFramework) RequiredExternalTools(ctx context.Context) []tools.ExternalTool {
	return []tools.ExternalTool{&fakeTool{}}
}

func (f *fakeFramework) Initialize(ctx context.Context, serviceConfig *ServiceConfig) error {
	return nil
}

func (f *fakeFramework) Restore(
	ctx context.Context,
	serviceConfig *ServiceConfig,
) *async.TaskWithProgress[*ServiceRestoreResult, ServiceProgress] {
	restoreCalled, ok := ctx.Value(frameworkRestoreCalled).(*bool)
	if ok {
		*restoreCalled = true
	}

	return async.RunTaskWithProgress(func(task *async.TaskContextWithProgress[*ServiceRestoreResult, ServiceProgress]) {
		runArgs := exec.NewRunArgs("fake-framework", "restore")
		result, err := f.commandRunner.Run(ctx, runArgs)
		if err != nil {
			task.SetError(err)
			return
		}

		task.SetResult(&ServiceRestoreResult{
			Details: result,
		})
	})
}

func (f *fakeFramework) Build(
	ctx context.Context,
	serviceConfig *ServiceConfig,
	restoreOutput *ServiceRestoreResult,
) *async.TaskWithProgress[*ServiceBuildResult, ServiceProgress] {
	buildCalled, ok := ctx.Value(frameworkBuildCalled).(*bool)
	if ok {
		*buildCalled = true
	}

	return async.RunTaskWithProgress(func(task *async.TaskContextWithProgress[*ServiceBuildResult, ServiceProgress]) {
		runArgs := exec.NewRunArgs("fake-framework", "build")
		result, err := f.commandRunner.Run(ctx, runArgs)
		if err != nil {
			task.SetError(err)
			return
		}

		task.SetResult(&ServiceBuildResult{
			Restore: restoreOutput,
			Details: result,
		})
	})
}

func (f *fakeFramework) Package(
	ctx context.Context,
	serviceConfig *ServiceConfig,
	buildOutput *ServiceBuildResult,
) *async.TaskWithProgress[*ServicePackageResult, ServiceProgress] {
	packageCalled, ok := ctx.Value(frameworkPackageCalled).(*bool)
	if ok {
		*packageCalled = true
	}

	return async.RunTaskWithProgress(func(task *async.TaskContextWithProgress[*ServicePackageResult, ServiceProgress]) {
		runArgs := exec.NewRunArgs("fake-framework", "package")
		result, err := f.commandRunner.Run(ctx, runArgs)
		if err != nil {
			task.SetError(err)
			return
		}

		task.SetResult(&ServicePackageResult{
			Build:   buildOutput,
			Details: result,
		})
	})
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

func (st *fakeServiceTarget) RequiredExternalTools(ctx context.Context) []tools.ExternalTool {
	return []tools.ExternalTool{&fakeTool{}}
}

func (st *fakeServiceTarget) Package(
	ctx context.Context,
	serviceConfig *ServiceConfig,
	packageOutput *ServicePackageResult,
) *async.TaskWithProgress[*ServicePackageResult, ServiceProgress] {
	packageCalled, ok := ctx.Value(serviceTargetPackageCalled).(*bool)
	if ok {
		*packageCalled = true
	}

	return async.RunTaskWithProgress(func(task *async.TaskContextWithProgress[*ServicePackageResult, ServiceProgress]) {
		runArgs := exec.NewRunArgs("fake-service-target", "package")
		result, err := st.commandRunner.Run(ctx, runArgs)
		if err != nil {
			task.SetError(err)
			return
		}

		task.SetResult(&ServicePackageResult{
			Build:   packageOutput.Build,
			Details: result,
		})
	})
}

func (st *fakeServiceTarget) Publish(
	ctx context.Context,
	serviceConfig *ServiceConfig,
	packageOutput *ServicePackageResult,
	targetResource *environment.TargetResource,
) *async.TaskWithProgress[*ServicePublishResult, ServiceProgress] {
	publishCalled, ok := ctx.Value(serviceTargetPublishCalled).(*bool)
	if ok {
		*publishCalled = true
	}

	return async.RunTaskWithProgress(func(task *async.TaskContextWithProgress[*ServicePublishResult, ServiceProgress]) {
		runArgs := exec.NewRunArgs("fake-service-target", "publish")
		result, err := st.commandRunner.Run(ctx, runArgs)
		if err != nil {
			task.SetError(err)
			return
		}

		task.SetResult(&ServicePublishResult{
			Package: packageOutput,
			Details: result,
		})
	})
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

func (t *fakeTool) CheckInstalled(ctx context.Context) (bool, error) {
	return true, nil
}
func (t *fakeTool) InstallUrl() string {
	return "https://aka.ms"
}
func (t *fakeTool) Name() string {
	return "fake tool"
}
