package terraform

import (
	"context"
	"fmt"
	"path"
	"strings"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/infra"
	. "github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning"
	"github.com/azure/azure-dev/cli/azd/test/mocks"

	execmock "github.com/azure/azure-dev/cli/azd/test/mocks/exec"
	"github.com/stretchr/testify/require"
)

func TestTerraformPlan(t *testing.T) {
	progressLog := []string{}
	interactiveLog := []bool{}
	progressDone := make(chan bool)

	mockContext := mocks.NewMockContext(context.Background())
	prepareGenericMocks(mockContext.CommandRunner)
	preparePlanningMocks(mockContext.CommandRunner)

	infraProvider := createTerraformProvider(*mockContext.Context)
	planningTask := infraProvider.Plan(*mockContext.Context)

	go func() {
		for progressReport := range planningTask.Progress() {
			progressLog = append(progressLog, progressReport.Message)
		}
		progressDone <- true
	}()

	go func() {
		for planningInteractive := range planningTask.Interactive() {
			interactiveLog = append(interactiveLog, planningInteractive)
		}
	}()

	deploymentPlan, err := planningTask.Await()
	<-progressDone

	require.Nil(t, err)
	require.NotNil(t, deploymentPlan.Deployment)

	consoleLog := mockContext.Console.Output()

	require.Len(t, consoleLog, 4)
	require.Contains(t, consoleLog[0], "Initializing terraform...")
	require.Contains(t, consoleLog[1], "Generating terraform parameters...")
	require.Contains(t, consoleLog[2], "Validating terraform template...")
	require.Contains(t, consoleLog[3], "Generating terraform plan...")

	require.Equal(t, infraProvider.env.Values["AZURE_LOCATION"], deploymentPlan.Deployment.Parameters["location"].Value)
	require.Equal(t, infraProvider.env.Values["AZURE_ENV_NAME"], deploymentPlan.Deployment.Parameters["name"].Value)

	require.NotNil(t, deploymentPlan.Details)

	terraformDeploymentData := deploymentPlan.Details.(TerraformDeploymentDetails)
	require.NotNil(t, terraformDeploymentData)

	require.FileExists(t, terraformDeploymentData.ParameterFilePath)
	require.NotEmpty(t, terraformDeploymentData.ParameterFilePath)
	require.NotEmpty(t, terraformDeploymentData.localStateFilePath)
}

func TestTerraformDeploy(t *testing.T) {
	progressLog := []string{}
	interactiveLog := []bool{}
	progressDone := make(chan bool)

	mockContext := mocks.NewMockContext(context.Background())
	prepareGenericMocks(mockContext.CommandRunner)
	preparePlanningMocks(mockContext.CommandRunner)
	prepareDeployMocks(mockContext.CommandRunner)

	infraProvider := createTerraformProvider(*mockContext.Context)

	envPath := path.Join(infraProvider.projectPath, ".azure", infraProvider.env.Values["AZURE_ENV_NAME"])

	deploymentPlan := DeploymentPlan{
		Details: TerraformDeploymentDetails{
			ParameterFilePath:  path.Join(envPath, "main.tfvars.json"),
			PlanFilePath:       path.Join(envPath, "main.tfplan"),
			localStateFilePath: path.Join(envPath, "terraform.tfstate"),
		},
	}

	scope := infra.NewSubscriptionScope(*mockContext.Context, infraProvider.env.Values["AZURE_LOCATION"], infraProvider.env.GetSubscriptionId(), infraProvider.env.GetEnvName())
	deployTask := infraProvider.Deploy(*mockContext.Context, &deploymentPlan, scope)

	go func() {
		for deployProgress := range deployTask.Progress() {
			progressLog = append(progressLog, deployProgress.Message)
		}
		progressDone <- true
	}()

	go func() {
		for deployInteractive := range deployTask.Interactive() {
			interactiveLog = append(interactiveLog, deployInteractive)
		}
	}()

	deployResult, err := deployTask.Await()
	<-progressDone

	require.Nil(t, err)
	require.NotNil(t, deployResult)

	require.Equal(t, deployResult.Deployment.Outputs["AZURE_LOCATION"].Value, infraProvider.env.Values["AZURE_LOCATION"])
	require.Equal(t, deployResult.Deployment.Outputs["RG_NAME"].Value, fmt.Sprintf("rg-%s", infraProvider.env.GetEnvName()))
}

func TestTerraformDestroy(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())
	prepareGenericMocks(mockContext.CommandRunner)
	preparePlanningMocks(mockContext.CommandRunner)
	prepareDestroyMocks(mockContext.CommandRunner)

	progressLog := []string{}
	interactiveLog := []bool{}
	progressDone := make(chan bool)

	infraProvider := createTerraformProvider(*mockContext.Context)
	deployment := Deployment{}

	destroyOptions := NewDestroyOptions(false, false)
	destroyTask := infraProvider.Destroy(*mockContext.Context, &deployment, destroyOptions)

	go func() {
		for destroyProgress := range destroyTask.Progress() {
			progressLog = append(progressLog, destroyProgress.Message)
		}
		progressDone <- true
	}()

	go func() {
		for destroyInteractive := range destroyTask.Interactive() {
			interactiveLog = append(interactiveLog, destroyInteractive)
		}
	}()

	destroyResult, err := destroyTask.Await()
	<-progressDone

	require.Nil(t, err)
	require.NotNil(t, destroyResult)

	require.Equal(t, destroyResult.Outputs["AZURE_LOCATION"].Value, infraProvider.env.Values["AZURE_LOCATION"])
	require.Equal(t, destroyResult.Outputs["RG_NAME"].Value, fmt.Sprintf("rg-%s", infraProvider.env.GetEnvName()))
}

func TestTerraformGetDeployment(t *testing.T) {
	progressLog := []string{}
	interactiveLog := []bool{}
	progressDone := make(chan bool)

	mockContext := mocks.NewMockContext(context.Background())
	prepareGenericMocks(mockContext.CommandRunner)
	preparePlanningMocks(mockContext.CommandRunner)
	prepareDeployMocks(mockContext.CommandRunner)

	infraProvider := createTerraformProvider(*mockContext.Context)
	scope := infra.NewSubscriptionScope(*mockContext.Context, infraProvider.env.Values["AZURE_LOCATION"], infraProvider.env.GetSubscriptionId(), infraProvider.env.GetEnvName())
	getDeploymentTask := infraProvider.GetDeployment(*mockContext.Context, scope)

	go func() {
		for progressReport := range getDeploymentTask.Progress() {
			progressLog = append(progressLog, progressReport.Message)
		}
		progressDone <- true
	}()

	go func() {
		for deploymentInteractive := range getDeploymentTask.Interactive() {
			interactiveLog = append(interactiveLog, deploymentInteractive)
		}
	}()

	getDeploymentResult, err := getDeploymentTask.Await()
	<-progressDone

	require.Nil(t, err)
	require.NotNil(t, getDeploymentResult.Deployment)

	require.Equal(t, getDeploymentResult.Deployment.Outputs["AZURE_LOCATION"].Value, infraProvider.env.Values["AZURE_LOCATION"])
	require.Equal(t, getDeploymentResult.Deployment.Outputs["RG_NAME"].Value, fmt.Sprintf("rg-%s", infraProvider.env.GetEnvName()))

}

func createTerraformProvider(ctx context.Context) *TerraformProvider {
	projectDir := "../../../../test/samples/resourcegroupterraform"
	options := Options{
		Module: "main",
	}

	env := environment.Environment{Values: make(map[string]string)}
	env.SetLocation("westus2")
	env.SetEnvName("test-env")

	return NewTerraformProvider(ctx, &env, projectDir, options)
}

func prepareGenericMocks(commandRunner *execmock.MockCommandRunner) {

	commandRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(command, "terraform version")
	}).Respond(exec.RunResult{
		Stdout: `{"terraform_version": "1.1.7"}`,
		Stderr: "",
	})
}

func preparePlanningMocks(commandRunner *execmock.MockCommandRunner) {
	modulePath := "..\\..\\..\\..\\test\\samples\\resourcegroupterraform\\infra"

	commandRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(command, fmt.Sprintf("terraform -chdir=%s init", modulePath))
	}).Respond(exec.RunResult{
		Stdout: string("Terraform has been successfully initialized!"),
		Stderr: "",
	})

	commandRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(command, fmt.Sprintf("terraform -chdir=%s validate", modulePath))
	}).Respond(exec.RunResult{
		Stdout: string("Success! The configuration is valid."),
		Stderr: "",
	})

	commandRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(command, fmt.Sprintf("terraform -chdir=%s plan", modulePath))
	}).Respond(exec.RunResult{
		Stdout: string("To perform exactly these actions, run the following command to apply:terraform apply"),
		Stderr: "",
	})
}

func prepareDeployMocks(commandRunner *execmock.MockCommandRunner) {
	modulePath := "..\\..\\..\\..\\test\\samples\\resourcegroupterraform\\infra"

	commandRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(command, fmt.Sprintf("terraform -chdir=%s validate", modulePath))
	}).Respond(exec.RunResult{
		Stdout: string("Success! The configuration is valid."),
		Stderr: "",
	})

	commandRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(command, fmt.Sprintf("terraform -chdir=%s apply", modulePath))
	}).Respond(exec.RunResult{
		Stdout: string(""),
		Stderr: "",
	})

	output := fmt.Sprintf("{\"AZURE_LOCATION\": {\"sensitive\": false,\"type\": \"string\",\"value\": \"westus2\"},\"RG_NAME\":{\"sensitive\": false,\"type\": \"string\",\"value\": \"rg-test-env\"}}")
	commandRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(command, fmt.Sprintf("terraform -chdir=%s output", modulePath))
	}).Respond(exec.RunResult{
		Stdout: output,
		Stderr: "",
	})
}

func prepareDestroyMocks(commandRunner *execmock.MockCommandRunner) {
	modulePath := "..\\..\\..\\..\\test\\samples\\resourcegroupterraform\\infra"

	commandRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(command, fmt.Sprintf("terraform -chdir=%s init", modulePath))
	}).Respond(exec.RunResult{
		Stdout: string("Terraform has been successfully initialized!"),
		Stderr: "",
	})

	output := fmt.Sprintf("{\"AZURE_LOCATION\": {\"sensitive\": false,\"type\": \"string\",\"value\": \"westus2\"},\"RG_NAME\":{\"sensitive\": false,\"type\": \"string\",\"value\": \"rg-test-env\"}}")
	commandRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(command, fmt.Sprintf("terraform -chdir=%s output", modulePath))
	}).Respond(exec.RunResult{
		Stdout: output,
		Stderr: "",
	})

	commandRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(command, fmt.Sprintf("terraform -chdir=%s destroy", modulePath))
	}).Respond(exec.RunResult{
		Stdout: string(""),
		Stderr: "",
	})

}
