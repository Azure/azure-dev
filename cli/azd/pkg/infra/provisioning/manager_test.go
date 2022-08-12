package provisioning

import (
	"context"
	"strings"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/azcli"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/bicep"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/stretchr/testify/require"
)

func TestManagerPreview(t *testing.T) {
	ctx := context.Background()
	env := environment.Environment{Values: make(map[string]string)}
	env.Values["AZURE_LOCATION"] = "eastus2"
	env.SetEnvName("test-env")
	options := Options{Provider: "test"}
	interactive := false
	execUtil := mocks.NewMockExecUtil()
	console := mocks.NewMockConsole()

	cliArgs := bicep.NewBicepCliArgs{
		AzCli:           azcli.NewAzCli(azcli.NewAzCliArgs{RunWithResultFn: execUtil.RunWithResult}),
		RunWithResultFn: execUtil.RunWithResult,
	}

	mgr, _ := NewManager(ctx, env, "", options, interactive, console, cliArgs)

	previewResult, err := mgr.Preview(ctx, false)

	require.NotNil(t, previewResult)
	require.Nil(t, err)
	require.Equal(t, previewResult.Deployment.Parameters["location"].Value, env.Values["AZURE_LOCATION"])
}

func TestManagerGetDeployment(t *testing.T) {
	ctx := context.Background()
	env := environment.Environment{Values: make(map[string]string)}
	env.Values["AZURE_LOCATION"] = "eastus2"
	env.SetEnvName("test-env")
	options := Options{Provider: "test"}
	interactive := false
	execUtil := mocks.NewMockExecUtil()
	console := mocks.NewMockConsole()

	cliArgs := bicep.NewBicepCliArgs{
		AzCli:           azcli.NewAzCli(azcli.NewAzCliArgs{RunWithResultFn: execUtil.RunWithResult}),
		RunWithResultFn: execUtil.RunWithResult,
	}

	mgr, _ := NewManager(ctx, env, "", options, interactive, console, cliArgs)

	provisioningScope := NewSubscriptionScope(cliArgs.AzCli, "eastus2", env.GetSubscriptionId(), env.GetEnvName())
	getResult, err := mgr.GetDeployment(ctx, provisioningScope, true)

	require.NotNil(t, getResult)
	require.Nil(t, err)
}

func TestManagerDeploy(t *testing.T) {
	ctx := context.Background()
	env := environment.Environment{Values: make(map[string]string)}
	env.Values["AZURE_LOCATION"] = "eastus2"
	env.SetEnvName("test-env")
	options := Options{Provider: "test"}
	interactive := false
	execUtil := mocks.NewMockExecUtil()
	console := mocks.NewMockConsole()

	cliArgs := bicep.NewBicepCliArgs{
		AzCli:           azcli.NewAzCli(azcli.NewAzCliArgs{RunWithResultFn: execUtil.RunWithResult}),
		RunWithResultFn: execUtil.RunWithResult,
	}

	mgr, _ := NewManager(ctx, env, "", options, interactive, console, cliArgs)

	previewResult, _ := mgr.Preview(ctx, false)
	deployOptions := DeployOptions{Interactive: true}
	provisioningScope := NewSubscriptionScope(cliArgs.AzCli, "eastus2", env.GetSubscriptionId(), env.GetEnvName())
	deployResult, err := mgr.Deploy(ctx, &previewResult.Deployment, provisioningScope, deployOptions)

	require.NotNil(t, deployResult)
	require.Nil(t, err)
}

func TestManagerDestroyWithPositiveConfirmation(t *testing.T) {
	ctx := context.Background()
	env := environment.Environment{Values: make(map[string]string)}
	env.Values["AZURE_LOCATION"] = "eastus2"
	env.SetEnvName("test-env")
	options := Options{Provider: "test"}
	interactive := false
	execUtil := mocks.NewMockExecUtil()
	console := mocks.NewMockConsole()

	console.WhenConfirm(func(options input.ConsoleOptions) bool {
		return strings.Contains(options.Message, "Are you sure you want to destroy?")
	}).Respond(true)

	cliArgs := bicep.NewBicepCliArgs{
		AzCli:           azcli.NewAzCli(azcli.NewAzCliArgs{RunWithResultFn: execUtil.RunWithResult}),
		RunWithResultFn: execUtil.RunWithResult,
	}

	mgr, _ := NewManager(ctx, env, "", options, interactive, console, cliArgs)

	previewResult, _ := mgr.Preview(ctx, false)
	destroyOptions := DestroyOptions{
		Interactive: true,
	}
	destroyResult, err := mgr.Destroy(ctx, &previewResult.Deployment, destroyOptions)

	require.NotNil(t, destroyResult)
	require.Nil(t, err)
	require.Contains(t, console.Output(), "Are you sure you want to destroy?")
}

func TestManagerDestroyWithNegativeConfirmation(t *testing.T) {
	ctx := context.Background()
	env := environment.Environment{Values: make(map[string]string)}
	env.Values["AZURE_LOCATION"] = "eastus2"
	env.SetEnvName("test-env")
	options := Options{Provider: "test"}
	interactive := false
	execUtil := mocks.NewMockExecUtil()
	console := mocks.NewMockConsole()

	console.WhenConfirm(func(options input.ConsoleOptions) bool {
		return strings.Contains(options.Message, "Are you sure you want to destroy?")
	}).Respond(false)

	cliArgs := bicep.NewBicepCliArgs{
		AzCli:           azcli.NewAzCli(azcli.NewAzCliArgs{RunWithResultFn: execUtil.RunWithResult}),
		RunWithResultFn: execUtil.RunWithResult,
	}

	mgr, _ := NewManager(ctx, env, "", options, interactive, console, cliArgs)

	previewResult, _ := mgr.Preview(ctx, false)
	destroyOptions := DestroyOptions{
		Interactive: true,
	}
	destroyResult, err := mgr.Destroy(ctx, &previewResult.Deployment, destroyOptions)

	require.Nil(t, destroyResult)
	require.NotNil(t, err)
	require.Contains(t, console.Output(), "Are you sure you want to destroy?")
}
