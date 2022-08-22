package provisioning

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/executil"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/azcli"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/bicep"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/stretchr/testify/require"
)

// Sets up all the mocks required for the bicep preview & deploy operation
func setupExecUtilWithMocks(template *BicepTemplate, deployResult *azcli.AzCliDeployment) *mocks.MockExecUtil {
	execUtil := &mocks.MockExecUtil{}

	bicepBytes, _ := json.Marshal(template)
	deployResultBytes, _ := json.Marshal(deployResult)

	// Setup expected values for executil
	execUtil.When(func(args executil.RunArgs) bool {
		return args.Cmd == "az" && args.Args[0] == "version"
	}).Respond(executil.RunResult{
		Stdout: `{"azure-cli": "2.38.0"}`,
		Stderr: "",
	})

	execUtil.When(func(args executil.RunArgs) bool {
		fullArgs := strings.Join(args.Args, " ")
		return args.Cmd == "az" && strings.Contains(fullArgs, "bicep build")
	}).Respond(executil.RunResult{
		Stdout: string(bicepBytes),
		Stderr: "",
	})

	// ARM deployment
	execUtil.When(func(args executil.RunArgs) bool {
		fullArgs := strings.Join(args.Args, " ")
		return args.Cmd == "az" && strings.Contains(fullArgs, "deployment sub create")
	}).Respond(executil.RunResult{
		Stdout: string(deployResultBytes),
		Stderr: "",
	})

	// Get deployment result
	execUtil.When(func(args executil.RunArgs) bool {
		fullArgs := strings.Join(args.Args, " ")
		return args.Cmd == "az" && strings.Contains(fullArgs, "deployment sub show")
	}).Respond(executil.RunResult{
		Stdout: string(deployResultBytes),
		Stderr: "",
	})

	// Gets deployment progress
	execUtil.When(
		func(args executil.RunArgs) bool {
			fullArgs := strings.Join(args.Args, " ")
			return args.Cmd == "az" && strings.Contains(fullArgs, "deployment operation sub list")
		}).Respond(executil.RunResult{
		Stdout: "",
		Stderr: "",
	})

	// Gets deployment progress
	execUtil.When(func(args executil.RunArgs) bool {
		fullArgs := strings.Join(args.Args, " ")
		return args.Cmd == "az" && strings.Contains(fullArgs, "deployment operation group list")
	}).Respond(executil.RunResult{
		Stdout: "",
		Stderr: "",
	})

	return execUtil
}

func TestBicepPreview(t *testing.T) {
	bicepInputParams := make(map[string]BicepInputParameter)
	bicepInputParams["name"] = BicepInputParameter{Value: "${AZURE_ENV_NAME}"}
	bicepInputParams["location"] = BicepInputParameter{Value: "${AZURE_LOCATION}"}

	bicepOutputParams := make(map[string]BicepOutputParameter)

	bicepTemplate := BicepTemplate{
		Parameters: bicepInputParams,
		Outputs:    bicepOutputParams,
	}

	execUtil := setupExecUtilWithMocks(&bicepTemplate, nil)
	azCli := azcli.NewAzCli(azcli.NewAzCliArgs{RunWithResultFn: execUtil.RunWithResult})
	projectDir := "../../../test/samples/webapp"
	options := Options{
		Module: "main",
	}

	env := environment.Environment{Values: make(map[string]string)}
	env.Values["AZURE_LOCATION"] = "eastus2"
	env.SetEnvName("test-env")

	bicepArgs := bicep.NewBicepCliArgs{AzCli: azCli, RunWithResultFn: execUtil.RunWithResult}
	console := &mocks.MockConsole{}
	infraProvider := NewBicepProvider(&env, projectDir, options, console, bicepArgs)
	previewTask := infraProvider.Preview(context.Background())

	go func() {
		for progressReport := range previewTask.Progress() {
			fmt.Println(progressReport.Timestamp)
		}
	}()

	go func() {
		for previewInteractive := range previewTask.Interactive() {
			fmt.Println(previewInteractive)
		}
	}()

	previewResult, err := previewTask.Await()

	require.Nil(t, err)
	require.NotNil(t, previewResult.Preview)

	require.Equal(t, env.Values["AZURE_LOCATION"], previewResult.Preview.Parameters["location"].Value)
	require.Equal(t, env.Values["AZURE_ENV_NAME"], previewResult.Preview.Parameters["name"].Value)
}

func TestBicepDeploy(t *testing.T) {
	expectedWebsiteUrl := "http://myapp.azurewebsites.net"
	bicepInputParams := make(map[string]BicepInputParameter)
	bicepInputParams["name"] = BicepInputParameter{Value: "${AZURE_ENV_NAME}"}
	bicepInputParams["location"] = BicepInputParameter{Value: "${AZURE_LOCATION}"}

	bicepOutputParams := make(map[string]BicepOutputParameter)

	bicepTemplate := BicepTemplate{
		Parameters: bicepInputParams,
		Outputs:    bicepOutputParams,
	}

	deployOutputs := make(map[string]azcli.AzCliDeploymentOutput)
	deployOutputs["WEBSITE_URL"] = azcli.AzCliDeploymentOutput{Value: expectedWebsiteUrl}
	azDeployment := azcli.AzCliDeployment{
		Id:   "DEPLOYMENT_ID",
		Name: "DEPLOYMENT_NAME",
		Properties: azcli.AzCliDeploymentProperties{
			Outputs: deployOutputs,
		},
	}

	execUtil := setupExecUtilWithMocks(&bicepTemplate, &azDeployment)
	ctx := context.Background()
	azCli := azcli.NewAzCli(azcli.NewAzCliArgs{RunWithResultFn: execUtil.RunWithResult})
	projectDir := "../../../test/samples/webapp"
	options := Options{
		Module: "main",
	}

	bicepArgs := bicep.NewBicepCliArgs{AzCli: azCli, RunWithResultFn: execUtil.RunWithResult}
	console := &mocks.MockConsole{}

	env := environment.Environment{Values: make(map[string]string)}
	env.Values["AZURE_LOCATION"] = "westus2"
	env.SetEnvName("test-env")

	scope := NewSubscriptionProvisioningScope(azCli, env.Values["AZURE_LOCATION"], env.GetSubscriptionId(), env.GetEnvName())
	infraProvider := NewBicepProvider(&env, projectDir, options, console, bicepArgs)
	previewTask := infraProvider.Preview(ctx)

	go func() {
		for previewProgress := range previewTask.Progress() {
			fmt.Println(previewProgress.Message)
		}
	}()

	go func() {
		for previewInteractive := range previewTask.Interactive() {
			fmt.Println(previewInteractive)
		}
	}()

	previewResult, err := previewTask.Await()
	require.Nil(t, err)
	require.NotNil(t, previewResult.Preview)

	deployProgressMsg := "Deploying..."
	fmt.Println(deployProgressMsg)
	deployTask := infraProvider.Deploy(ctx, &previewResult.Preview, scope)

	go func() {
		for deployProgress := range deployTask.Progress() {
			fmt.Println(deployProgress.Timestamp)
		}
	}()

	go func() {
		for deployInteractive := range deployTask.Interactive() {
			fmt.Println(deployInteractive)
		}
	}()

	deployResult, err := deployTask.Await()
	require.Nil(t, err)
	require.NotNil(t, deployResult)
	require.Equal(t, deployResult.Outputs["WEBSITE_URL"].Value, expectedWebsiteUrl)
}
