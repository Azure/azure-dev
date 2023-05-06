package terraform

import (
	"context"
	_ "embed"
	"fmt"
	"path"
	"regexp"
	"strings"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/account"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	. "github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning"
	"github.com/azure/azure-dev/cli/azd/test/mocks"

	"github.com/azure/azure-dev/cli/azd/test/mocks/mockexec"
	"github.com/stretchr/testify/require"
)

func TestTerraformPlan(t *testing.T) {
	progressLog := []string{}
	interactiveLog := []bool{}
	progressDone := make(chan bool)

	mockContext := mocks.NewMockContext(context.Background())
	prepareGenericMocks(mockContext.CommandRunner)
	preparePlanningMocks(mockContext.CommandRunner)

	infraProvider := createTerraformProvider(mockContext)
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

	require.Len(t, consoleLog, 0)

	require.Equal(t, infraProvider.env.Values["AZURE_LOCATION"], deploymentPlan.Deployment.Parameters["location"].Value)
	require.Equal(
		t,
		infraProvider.env.Values["AZURE_ENV_NAME"],
		deploymentPlan.Deployment.Parameters["environment_name"].Value,
	)

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

	infraProvider := createTerraformProvider(mockContext)

	envPath := path.Join(infraProvider.projectPath, ".azure", infraProvider.env.Values["AZURE_ENV_NAME"])

	deploymentPlan := DeploymentPlan{
		Details: TerraformDeploymentDetails{
			ParameterFilePath:  path.Join(envPath, "main.tfvars.json"),
			PlanFilePath:       path.Join(envPath, "main.tfplan"),
			localStateFilePath: path.Join(envPath, "terraform.tfstate"),
		},
	}

	deployTask := infraProvider.Deploy(*mockContext.Context, &deploymentPlan)

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

	infraProvider := createTerraformProvider(mockContext)
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

func TestTerraformState(t *testing.T) {
	progressLog := []string{}
	interactiveLog := []bool{}
	progressDone := make(chan bool)

	mockContext := mocks.NewMockContext(context.Background())
	prepareGenericMocks(mockContext.CommandRunner)
	prepareShowMocks(mockContext.CommandRunner)

	infraProvider := createTerraformProvider(mockContext)
	getStateTask := infraProvider.State(*mockContext.Context)

	go func() {
		for progressReport := range getStateTask.Progress() {
			progressLog = append(progressLog, progressReport.Message)
		}
		progressDone <- true
	}()

	go func() {
		for deploymentInteractive := range getStateTask.Interactive() {
			interactiveLog = append(interactiveLog, deploymentInteractive)
		}
	}()

	getStateResult, err := getStateTask.Await()
	<-progressDone

	require.Nil(t, err)
	require.NotNil(t, getStateResult.State)

	require.Equal(t, infraProvider.env.Values["AZURE_LOCATION"], getStateResult.State.Outputs["AZURE_LOCATION"].Value)
	require.Equal(t, fmt.Sprintf("rg-%s", infraProvider.env.GetEnvName()), getStateResult.State.Outputs["RG_NAME"].Value)
	require.Len(t, getStateResult.State.Resources, 1)
	require.Regexp(
		t,
		regexp.MustCompile(`^/subscriptions/[^/]*/resourceGroups/[^/]*$`),
		getStateResult.State.Resources[0].Id,
	)
}

func createTerraformProvider(mockContext *mocks.MockContext) *TerraformProvider {
	projectDir := "../../../../test/functional/testdata/samples/resourcegroupterraform"
	options := Options{
		Module: "main",
	}

	env := environment.EphemeralWithValues("test-env", map[string]string{
		"AZURE_LOCATION":        "westus2",
		"AZURE_SUBSCRIPTION_ID": "00000000-0000-0000-0000-000000000000",
	})

	return NewTerraformProvider(
		*mockContext.Context,
		env,
		projectDir,
		options,
		mockContext.Console,
		mockContext.CommandRunner,
		&mockCurrentPrincipal{},
		Prompters{
			Location: func(_ context.Context, _, _ string, _ func(loc account.Location) bool) (location string, err error) {
				return "westus2", nil
			},
			Subscription: func(_ context.Context, _ string) (subscriptionId string, err error) {
				return "00000000-0000-0000-0000-000000000000", nil
			},
			EnsureSubscriptionLocation: func(ctx context.Context, env *environment.Environment) error {
				env.SetSubscriptionId("00000000-0000-0000-0000-000000000000")
				env.SetLocation("westus2")
				return nil
			},
		},
	)
}

func prepareGenericMocks(commandRunner *mockexec.MockCommandRunner) {
	commandRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(command, "terraform version")
	}).Respond(exec.RunResult{
		Stdout: `{"terraform_version": "1.1.7"}`,
		Stderr: "",
	})

}

func preparePlanningMocks(commandRunner *mockexec.MockCommandRunner) {
	commandRunner.When(func(args exec.RunArgs, command string) bool {
		return args.Cmd == "terraform" && strings.Contains(command, "init")
	}).Respond(exec.RunResult{
		Stdout: "Terraform has been successfully initialized!",
		Stderr: "",
	})

	commandRunner.When(func(args exec.RunArgs, command string) bool {
		return args.Cmd == "terraform" && strings.Contains(command, "validate")
	}).Respond(exec.RunResult{
		Stdout: "Success! The configuration is valid.",
		Stderr: "",
	})

	commandRunner.When(func(args exec.RunArgs, command string) bool {
		return args.Cmd == "terraform" && strings.Contains(command, "plan")
	}).Respond(exec.RunResult{
		Stdout: "To perform exactly these actions, run the following command to apply:terraform apply",
		Stderr: "",
	})
}

func prepareDeployMocks(commandRunner *mockexec.MockCommandRunner) {
	commandRunner.When(func(args exec.RunArgs, command string) bool {
		return args.Cmd == "terraform" && strings.Contains(command, "validate")
	}).Respond(exec.RunResult{
		Stdout: "Success! The configuration is valid.",
		Stderr: "",
	})

	commandRunner.When(func(args exec.RunArgs, command string) bool {
		return args.Cmd == "terraform" && strings.Contains(command, "apply")
	}).Respond(exec.RunResult{
		Stdout: "",
		Stderr: "",
	})

	//nolint:lll
	output := `{"AZURE_LOCATION":{"sensitive": false,"type": "string","value": "westus2"},"RG_NAME":{"sensitive": false,"type": "string","value": "rg-test-env"}}`
	commandRunner.When(func(args exec.RunArgs, command string) bool {
		return args.Cmd == "terraform" && strings.Contains(command, "output")
	}).Respond(exec.RunResult{
		Stdout: output,
		Stderr: "",
	})
}

//go:embed testdata/terraform_show_mock.json
var terraformShowMockOutput string

func prepareShowMocks(commandRunner *mockexec.MockCommandRunner) {
	commandRunner.When(func(args exec.RunArgs, command string) bool {
		return args.Cmd == "terraform" && strings.Contains(command, "show")
	}).Respond(exec.RunResult{
		Stdout: terraformShowMockOutput,
		Stderr: "",
	})
}

func prepareDestroyMocks(commandRunner *mockexec.MockCommandRunner) {
	commandRunner.When(func(args exec.RunArgs, command string) bool {
		return args.Cmd == "terraform" && strings.Contains(command, "init")
	}).Respond(exec.RunResult{
		Stdout: "Terraform has been successfully initialized!",
		Stderr: "",
	})

	//nolint:lll
	output := `{"AZURE_LOCATION":{"sensitive": false,"type": "string","value": "westus2"},"RG_NAME":{"sensitive": false,"type": "string","value": "rg-test-env"}}`
	commandRunner.When(func(args exec.RunArgs, command string) bool {
		return args.Cmd == "terraform" && strings.Contains(command, "output")
	}).Respond(exec.RunResult{
		Stdout: output,
		Stderr: "",
	})

	commandRunner.When(func(args exec.RunArgs, command string) bool {
		return args.Cmd == "terraform" && strings.Contains(command, "destroy")
	}).Respond(exec.RunResult{
		Stdout: "",
		Stderr: "",
	})
}

type mockCurrentPrincipal struct{}

func (m *mockCurrentPrincipal) CurrentPrincipalId(_ context.Context) (string, error) {
	return "11111111-1111-1111-1111-111111111111", nil
}
