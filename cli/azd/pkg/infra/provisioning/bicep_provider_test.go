// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

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
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	execmock "github.com/azure/azure-dev/cli/azd/test/mocks/executil"
	"github.com/stretchr/testify/require"
)

func TestBicepPreview(t *testing.T) {
	progressLog := []string{}
	interactiveLog := []bool{}
	progressDone := make(chan bool)

	mockContext := mocks.NewMockContext(context.Background())
	prepareGenericMocks(mockContext.CommandRunner)
	preparePreviewMocks(mockContext.CommandRunner)

	infraProvider := createBicepProvider(*mockContext.Context)
	previewTask := infraProvider.Preview(*mockContext.Context)

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

	mockContext := mocks.NewMockContext(context.Background())
	prepareGenericMocks(mockContext.CommandRunner)
	preparePreviewMocks(mockContext.CommandRunner)
	prepareDeployMocks(mockContext.CommandRunner)

	infraProvider := createBicepProvider(*mockContext.Context)
	scope := NewSubscriptionScope(*mockContext.Context, infraProvider.env.Values["AZURE_LOCATION"], infraProvider.env.GetSubscriptionId(), infraProvider.env.GetEnvName())
	getDeploymentTask := infraProvider.GetDeployment(*mockContext.Context, scope)

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

	mockContext := mocks.NewMockContext(context.Background())
	prepareGenericMocks(mockContext.CommandRunner)
	preparePreviewMocks(mockContext.CommandRunner)
	prepareDeployMocks(mockContext.CommandRunner)

	infraProvider := createBicepProvider(*mockContext.Context)
	deployment := Deployment{}

	scope := NewSubscriptionScope(*mockContext.Context, infraProvider.env.Values["AZURE_LOCATION"], infraProvider.env.GetSubscriptionId(), infraProvider.env.GetEnvName())
	deployTask := infraProvider.Deploy(*mockContext.Context, &deployment, scope)

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
	require.Equal(t, 1, len(mockContext.Console.Output()))
	require.True(t, strings.Contains(mockContext.Console.Output()[0], "Provisioning Azure resources"))
}

func TestBicepDestroy(t *testing.T) {
	t.Run("Interactive", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		prepareGenericMocks(mockContext.CommandRunner)
		preparePreviewMocks(mockContext.CommandRunner)
		prepareDestroyMocks(mockContext.CommandRunner)

		progressLog := []string{}
		interactiveLog := []bool{}
		progressDone := make(chan bool)

		// Setup console mocks
		mockContext.Console.WhenConfirm(func(options input.ConsoleOptions) bool {
			return strings.Contains(options.Message, "This will delete")
		}).Respond(true)

		mockContext.Console.WhenConfirm(func(options input.ConsoleOptions) bool {
			return strings.Contains(options.Message, "Would you like to permanently delete these Key Vaults")
		}).Respond(true)

		infraProvider := createBicepProvider(*mockContext.Context)
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

		// Verify console prompts
		consoleOutput := mockContext.Console.Output()
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
		mockContext := mocks.NewMockContext(context.Background())
		prepareGenericMocks(mockContext.CommandRunner)
		preparePreviewMocks(mockContext.CommandRunner)
		prepareDestroyMocks(mockContext.CommandRunner)

		progressLog := []string{}
		interactiveLog := []bool{}
		progressDone := make(chan bool)

		infraProvider := createBicepProvider(*mockContext.Context)
		deployment := Deployment{}

		destroyOptions := NewDestroyOptions(true, true)
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

		// Verify console prompts
		consoleOutput := mockContext.Console.Output()
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

func createBicepProvider(ctx context.Context) *BicepProvider {
	projectDir := "../../../test/samples/webapp"
	options := Options{
		Module: "main",
	}

	env := environment.Environment{Values: make(map[string]string)}
	env.SetLocation("westus2")
	env.SetEnvName("test-env")

	return NewBicepProvider(ctx, &env, projectDir, options)
}

func prepareGenericMocks(execUtil *execmock.MockCommandRunner) {
	// Setup expected values for executil
	execUtil.When(func(args executil.RunArgs, command string) bool {
		return strings.Contains(command, "az version")
	}).Respond(executil.RunResult{
		Stdout: `{"azure-cli": "2.38.0"}`,
		Stderr: "",
	})
}

// Sets up all the mocks required for the bicep preview & deploy operation
func prepareDeployMocks(execUtil *execmock.MockCommandRunner) {
	// Gets deployment progress
	execUtil.When(
		func(args executil.RunArgs, command string) bool {
			return strings.Contains(command, "az deployment operation sub list")
		}).Respond(executil.RunResult{
		Stdout: "",
		Stderr: "",
	})

	// Gets deployment progress
	execUtil.When(func(args executil.RunArgs, command string) bool {
		return strings.Contains(command, "deployment operation group list")
	}).Respond(executil.RunResult{
		Stdout: "",
		Stderr: "",
	})
}

func preparePreviewMocks(execUtil *execmock.MockCommandRunner) {
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

	execUtil.When(func(args executil.RunArgs, command string) bool {
		return strings.Contains(command, "az bicep build")
	}).Respond(executil.RunResult{
		Stdout: string(bicepBytes),
		Stderr: "",
	})

	// ARM deployment
	execUtil.When(func(args executil.RunArgs, command string) bool {
		return strings.Contains(command, "az deployment sub create")
	}).Respond(executil.RunResult{
		Stdout: string(deployResultBytes),
		Stderr: "",
	})

	// Get deployment result
	execUtil.When(func(args executil.RunArgs, command string) bool {
		return strings.Contains(command, "az deployment sub show")
	}).Respond(executil.RunResult{
		Stdout: string(deployResultBytes),
		Stderr: "",
	})
}

func prepareDestroyMocks(execUtil *execmock.MockCommandRunner) {
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
	execUtil.When(func(args executil.RunArgs, command string) bool {
		return strings.Contains(command, "az resource list")
	}).Respond(executil.RunResult{
		Stdout: string(resourceListBytes),
		Stderr: "",
	})

	// Get Key Vault
	execUtil.When(func(args executil.RunArgs, command string) bool {
		return strings.Contains(command, "az keyvault show")
	}).Respond(executil.RunResult{
		Stdout: string(keyVaultBytes),
		Stderr: "",
	})

	// Delete resource group
	execUtil.When(func(args executil.RunArgs, command string) bool {
		return strings.Contains(command, "az group delete")
	}).Respond(executil.RunResult{
		Stdout: "",
		Stderr: "",
	})

	// Purge Key vault
	execUtil.When(func(args executil.RunArgs, command string) bool {
		return strings.Contains(command, "az keyvault purge")
	}).Respond(executil.RunResult{
		Stdout: "",
		Stderr: "",
	})

	// Delete deployment
	execUtil.When(func(args executil.RunArgs, command string) bool {
		return strings.Contains(command, "az deployment sub delete")
	}).Respond(executil.RunResult{
		Stdout: "",
		Stderr: "",
	})
}
