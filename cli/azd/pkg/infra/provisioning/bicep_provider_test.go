package provisioning

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/executil"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/stretchr/testify/require"
)

// Sets up all the mocks required for the bicep plan & apply operation
func setupExecUtilWithMocks(template *BicepTemplate, deployResult *tools.AzCliDeployment) *mocks.MockExecUtil {
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

func TestBicepPlan(t *testing.T) {
	bicepInputParams := make(map[string]BicepInputParameter)
	bicepInputParams["name"] = BicepInputParameter{Value: "${AZURE_ENV_NAME}"}
	bicepInputParams["location"] = BicepInputParameter{Value: "${AZURE_LOCATION}"}

	bicepOutputParams := make(map[string]BicepOutputParameter)

	bicepTemplate := BicepTemplate{
		Parameters: bicepInputParams,
		Outputs:    bicepOutputParams,
	}

	execUtil := setupExecUtilWithMocks(&bicepTemplate, nil)
	azCli := tools.NewAzCli(tools.NewAzCliArgs{RunWithResultFn: execUtil.RunWithResult})
	projectDir := "../../../test/samples/webapp"
	options := Options{
		Module: "main",
	}

	env := environment.Environment{Values: make(map[string]string)}
	env.Values["AZURE_LOCATION"] = "eastus2"
	env.SetEnvName("test-env")

	bicepArgs := tools.NewBicepCliArgs{AzCli: azCli, RunWithResultFn: execUtil.RunWithResult}
	console := &mocks.MockConsole{}
	infraProvider := NewBicepProvider(&env, projectDir, options, console, bicepArgs)
	planTask := infraProvider.Plan(context.Background())

	go func() {
		for progressReport := range planTask.Progress() {
			fmt.Println(progressReport.Timestamp)
		}
	}()

	go func() {
		for planInteractive := range planTask.Interactive() {
			fmt.Println(planInteractive)
		}
	}()

	planResult := planTask.Result()

	require.Nil(t, planTask.Error)
	require.NotNil(t, planResult.Plan)

	require.Equal(t, env.Values["AZURE_LOCATION"], planResult.Plan.Parameters["location"].Value)
	require.Equal(t, env.Values["AZURE_ENV_NAME"], planResult.Plan.Parameters["name"].Value)
}

func TestBicepApply(t *testing.T) {
	expectedWebsiteUrl := "http://myapp.azurewebsites.net"
	bicepInputParams := make(map[string]BicepInputParameter)
	bicepInputParams["name"] = BicepInputParameter{Value: "${AZURE_ENV_NAME}"}
	bicepInputParams["location"] = BicepInputParameter{Value: "${AZURE_LOCATION}"}

	bicepOutputParams := make(map[string]BicepOutputParameter)

	bicepTemplate := BicepTemplate{
		Parameters: bicepInputParams,
		Outputs:    bicepOutputParams,
	}

	deployOutputs := make(map[string]tools.AzCliDeploymentOutput)
	deployOutputs["WEBSITE_URL"] = tools.AzCliDeploymentOutput{Value: expectedWebsiteUrl}
	deployResult := tools.AzCliDeployment{
		Id:   "DEPLOYMENT_ID",
		Name: "DEPLOYMENT_NAME",
		Properties: tools.AzCliDeploymentProperties{
			Outputs: deployOutputs,
		},
	}

	execUtil := setupExecUtilWithMocks(&bicepTemplate, &deployResult)
	ctx := context.Background()
	azCli := tools.NewAzCli(tools.NewAzCliArgs{RunWithResultFn: execUtil.RunWithResult})
	projectDir := "../../../test/samples/webapp"
	options := Options{
		Module: "main",
	}

	bicepArgs := tools.NewBicepCliArgs{AzCli: azCli, RunWithResultFn: execUtil.RunWithResult}
	console := &mocks.MockConsole{}

	env := environment.Environment{Values: make(map[string]string)}
	env.Values["AZURE_LOCATION"] = "westus2"
	env.SetEnvName("test-env")

	scope := NewSubscriptionProvisioningScope(azCli, env.Values["AZURE_LOCATION"], env.GetSubscriptionId(), env.GetEnvName())
	infraProvider := NewBicepProvider(&env, projectDir, options, console, bicepArgs)
	planTask := infraProvider.Plan(ctx)

	go func() {
		for planProgress := range planTask.Progress() {
			fmt.Println(planProgress.Message)
		}
	}()

	go func() {
		for planInteractive := range planTask.Interactive() {
			fmt.Println(planInteractive)
		}
	}()

	require.Nil(t, planTask.Error)
	planResult := planTask.Result()
	require.NotNil(t, planResult.Plan)

	applyProgressMsg := "Deploying..."
	fmt.Println(applyProgressMsg)
	applyTask := infraProvider.Apply(ctx, &planResult.Plan, scope)

	go func() {
		for applyProgress := range applyTask.Progress() {
			fmt.Println(applyProgress.Timestamp)
		}
	}()

	go func() {
		for applyInteractive := range applyTask.Interactive() {
			fmt.Println(applyInteractive)
		}
	}()

	applyResult := applyTask.Result()
	require.NotNil(t, applyResult)
	require.Equal(t, applyResult.Outputs["WEBSITE_URL"].Value, expectedWebsiteUrl)
}
