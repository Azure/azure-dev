package provisioning

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/executil"
	"github.com/azure/azure-dev/cli/azd/pkg/infra"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/azcli"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/bicep"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/stretchr/testify/require"
)

func TestBicepPreview(t *testing.T) {
	progressLog := []string{}
	interactiveLog := []bool{}
	progressDone := make(chan bool)

	execUtil := mocks.NewMockExecUtil()
	prepareGenericMocks(execUtil)
	preparePreviewMocks(execUtil)

	console := &mocks.MockConsole{}
	infraProvider := createBicepProvider(execUtil, console)

	previewTask := infraProvider.Preview(context.Background())

	go func() {
		for progressReport := range previewTask.Progress() {
			progressLog = append(progressLog, progressReport.Message)
		}
		progressDone <- true
	}()

	go func() {
		for previewInteractive := range previewTask.Interactive() {
			interactiveLog = append(interactiveLog, previewInteractive)
		}
	}()

	previewResult, err := previewTask.Await()
	<-progressDone

	require.Nil(t, err)
	require.NotNil(t, previewResult.Deployment)

	require.Len(t, progressLog, 2)
	require.Contains(t, progressLog[0], "Generating Bicep parameters file")
	require.Contains(t, progressLog[1], "Compiling Bicep template")

	require.Equal(t, infraProvider.env.Values["AZURE_LOCATION"], previewResult.Deployment.Parameters["location"].Value)
	require.Equal(t, infraProvider.env.Values["AZURE_ENV_NAME"], previewResult.Deployment.Parameters["name"].Value)
}

func TestBicepGetDeploymentPreview(t *testing.T) {
	progressLog := []string{}
	interactiveLog := []bool{}
	progressDone := make(chan bool)
	expectedWebsiteUrl := "http://myapp.azurewebsites.net"

	execUtil := mocks.NewMockExecUtil()
	prepareGenericMocks(execUtil)
	preparePreviewMocks(execUtil)
	prepareDeployMocks(execUtil)

	console := &mocks.MockConsole{}
	infraProvider := createBicepProvider(execUtil, console)
	scope := NewSubscriptionScope(infraProvider.azCli, infraProvider.env.Values["AZURE_LOCATION"], infraProvider.env.GetSubscriptionId(), infraProvider.env.GetEnvName())
	getDeploymentTask := infraProvider.GetDeployment(context.Background(), scope)

	go func() {
		for progressReport := range getDeploymentTask.Progress() {
			progressLog = append(progressLog, progressReport.Message)
		}
		progressDone <- true
	}()

	go func() {
		for previewInteractive := range getDeploymentTask.Interactive() {
			interactiveLog = append(interactiveLog, previewInteractive)
		}
	}()

	getDeploymentResult, err := getDeploymentTask.Await()
	<-progressDone

	require.Nil(t, err)
	require.NotNil(t, getDeploymentResult.Deployment)
	require.Equal(t, getDeploymentResult.Deployment.Outputs["WEBSITE_URL"].Value, expectedWebsiteUrl)

	require.Len(t, progressLog, 3)
	require.Contains(t, progressLog[0], "Loading Bicep template")
	require.Contains(t, progressLog[1], "Retrieving Azure deployment")
	require.Contains(t, progressLog[2], "Normalizing output parameters")
}

func TestBicepDeploy(t *testing.T) {
	expectedWebsiteUrl := "http://myapp.azurewebsites.net"
	progressLog := []string{}
	interactiveLog := []bool{}
	progressDone := make(chan bool)

	execUtil := mocks.NewMockExecUtil()
	prepareGenericMocks(execUtil)
	preparePreviewMocks(execUtil)
	prepareDeployMocks(execUtil)

	console := mocks.NewMockConsole()
	infraProvider := createBicepProvider(execUtil, console)
	deployment := Deployment{}

	scope := NewSubscriptionScope(infraProvider.azCli, infraProvider.env.Values["AZURE_LOCATION"], infraProvider.env.GetSubscriptionId(), infraProvider.env.GetEnvName())
	deployTask := infraProvider.Deploy(context.Background(), &deployment, scope)

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
	require.Equal(t, deployResult.Deployment.Outputs["WEBSITE_URL"].Value, expectedWebsiteUrl)
	require.Equal(t, 1, len(console.Output()))
	require.True(t, strings.Contains(console.Output()[0], "Provisioning Azure resources"))
}

func TestBicepDestroy(t *testing.T) {
	execUtil := mocks.NewMockExecUtil()
	prepareGenericMocks(execUtil)
	preparePreviewMocks(execUtil)
	prepareDestroyMocks(execUtil)

	t.Run("Interactive", func(t *testing.T) {
		progressLog := []string{}
		interactiveLog := []bool{}
		progressDone := make(chan bool)

		// Setup console mocks
		console := mocks.NewMockConsole()
		console.WhenConfirm(func(options input.ConsoleOptions) bool {
			return strings.Contains(options.Message, "This will delete")
		}).Respond(true)

		console.WhenConfirm(func(options input.ConsoleOptions) bool {
			return strings.Contains(options.Message, "Would you like to permanently delete these Key Vaults")
		}).Respond(true)

		infraProvider := createBicepProvider(execUtil, console)
		deployment := Deployment{}

		destroyOptions := DestroyOptions{Interactive: true}
		destroyTask := infraProvider.Destroy(context.Background(), &deployment, destroyOptions)

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

		// Verify console prompts
		consoleOutput := console.Output()
		require.Len(t, consoleOutput, 6)
		require.Contains(t, consoleOutput[0], "This will delete")
		require.Contains(t, consoleOutput[1], "Deleted resource group")
		require.Contains(t, consoleOutput[2], "This operation will delete and purge")
		require.Contains(t, consoleOutput[3], "Would you like to permanently delete these Key Vaults")
		require.Contains(t, consoleOutput[4], "Purged key vault")
		require.Contains(t, consoleOutput[5], "Deleted deployment")

		// Verify progress output
		require.Len(t, progressLog, 5)
		require.Contains(t, progressLog[0], "Fetching resource groups")
		require.Contains(t, progressLog[1], "Fetching resources")
		require.Contains(t, progressLog[2], "Deleting resource group")
		require.Contains(t, progressLog[3], "Purging key vault")
		require.Contains(t, progressLog[4], "Deleting deployment")
	})

	t.Run("InteractiveForceAndPurge", func(t *testing.T) {
		progressLog := []string{}
		interactiveLog := []bool{}
		progressDone := make(chan bool)

		// Setup console mocks
		console := mocks.NewMockConsole()

		infraProvider := createBicepProvider(execUtil, console)
		deployment := Deployment{}

		destroyOptions := DestroyOptions{Interactive: true, Force: true, Purge: true}
		destroyTask := infraProvider.Destroy(context.Background(), &deployment, destroyOptions)

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

		// Verify console prompts
		consoleOutput := console.Output()
		require.Len(t, consoleOutput, 3)
		require.Contains(t, consoleOutput[0], "Deleted resource group")
		require.Contains(t, consoleOutput[1], "Purged key vault")
		require.Contains(t, consoleOutput[2], "Deleted deployment")

		// Verify progress output
		require.Len(t, progressLog, 5)
		require.Contains(t, progressLog[0], "Fetching resource groups")
		require.Contains(t, progressLog[1], "Fetching resources")
		require.Contains(t, progressLog[2], "Deleting resource group")
		require.Contains(t, progressLog[3], "Purging key vault")
		require.Contains(t, progressLog[4], "Deleting deployment")
	})
}

func createBicepProvider(execUtil *mocks.MockExecUtil, console input.Console) *BicepProvider {
	azCli := azcli.NewAzCli(azcli.NewAzCliArgs{RunWithResultFn: execUtil.RunWithResult})
	projectDir := "../../../test/samples/webapp"
	options := Options{
		Module: "main",
	}

	bicepArgs := bicep.NewBicepCliArgs{AzCli: azCli, RunWithResultFn: execUtil.RunWithResult}

	env := environment.Environment{Values: make(map[string]string)}
	env.SetLocation("westus2")
	env.SetEnvName("test-env")

	return NewBicepProvider(&env, projectDir, options, console, bicepArgs)
}

func prepareGenericMocks(execUtil *mocks.MockExecUtil) {
	// Setup expected values for executil
	execUtil.When(func(args executil.RunArgs) bool {
		return args.Cmd == "az" && args.Args[0] == "version"
	}).Respond(executil.RunResult{
		Stdout: `{"azure-cli": "2.38.0"}`,
		Stderr: "",
	})
}

// Sets up all the mocks required for the bicep preview & deploy operation
func prepareDeployMocks(execUtil *mocks.MockExecUtil) {
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
}

func preparePreviewMocks(execUtil *mocks.MockExecUtil) {
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
			Dependencies: []azcli.AzCliDeploymentPropertiesDependency{
				{
					DependsOn: []azcli.AzCliDeploymentPropertiesBasicDependency{
						{
							Id:           "RESOURCE_ID",
							ResourceName: "RESOURCE_GROUP",
							ResourceType: string(infra.AzureResourceTypeResourceGroup),
						},
					},
				},
			},
		},
	}

	bicepBytes, _ := json.Marshal(bicepTemplate)
	deployResultBytes, _ := json.Marshal(azDeployment)

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
}

func prepareDestroyMocks(execUtil *mocks.MockExecUtil) {
	resourceList := []azcli.AzCliResource{
		{
			Id:   "webapp",
			Name: "app-123",
			Type: string(infra.AzureResourceTypeWebSite),
		},
		{
			Id:   "keyvault",
			Name: "kv-123",
			Type: string(infra.AzureResourceTypeKeyVault),
		},
	}

	resourceListBytes, _ := json.Marshal(resourceList)

	keyVault := azcli.AzCliKeyVault{
		Id:   "kv-123",
		Name: "kv-123",
		Properties: struct {
			EnableSoftDelete      bool "json:\"enableSoftDelete\""
			EnablePurgeProtection bool "json:\"enablePurgeProtection\""
		}{
			EnableSoftDelete:      true,
			EnablePurgeProtection: false,
		},
	}

	keyVaultBytes, _ := json.Marshal(keyVault)

	// Get list of resources to delete
	execUtil.When(func(args executil.RunArgs) bool {
		fullArgs := strings.Join(args.Args, " ")
		return args.Cmd == "az" && strings.Contains(fullArgs, "resource list")
	}).Respond(executil.RunResult{
		Stdout: string(resourceListBytes),
		Stderr: "",
	})

	// Get Key Vault
	execUtil.When(func(args executil.RunArgs) bool {
		fullArgs := strings.Join(args.Args, " ")
		return args.Cmd == "az" && strings.Contains(fullArgs, "keyvault show")
	}).Respond(executil.RunResult{
		Stdout: string(keyVaultBytes),
		Stderr: "",
	})

	// Delete resource group
	execUtil.When(func(args executil.RunArgs) bool {
		fullArgs := strings.Join(args.Args, " ")
		return args.Cmd == "az" && strings.Contains(fullArgs, "group delete")
	}).Respond(executil.RunResult{
		Stdout: "",
		Stderr: "",
	})

	// Purge Key vault
	execUtil.When(func(args executil.RunArgs) bool {
		fullArgs := strings.Join(args.Args, " ")
		return args.Cmd == "az" && strings.Contains(fullArgs, "keyvault purge")
	}).Respond(executil.RunResult{
		Stdout: "",
		Stderr: "",
	})

	// Delete deployment
	execUtil.When(func(args executil.RunArgs) bool {
		fullArgs := strings.Join(args.Args, " ")
		return args.Cmd == "az" && strings.Contains(fullArgs, "deployment sub delete")
	}).Respond(executil.RunResult{
		Stdout: "",
		Stderr: "",
	})
}
