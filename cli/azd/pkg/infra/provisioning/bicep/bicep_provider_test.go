// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package bicep

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path"
	"strings"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/appconfiguration/armappconfiguration"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/keyvault/armkeyvault"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
	"github.com/azure/azure-dev/cli/azd/pkg/azure"
	"github.com/azure/azure-dev/cli/azd/pkg/convert"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/infra"
	. "github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	execmock "github.com/azure/azure-dev/cli/azd/test/mocks/exec"
	"github.com/azure/azure-dev/cli/azd/test/mocks/httputil"
	"github.com/stretchr/testify/require"
)

func TestBicepPlan(t *testing.T) {
	progressLog := []string{}
	interactiveLog := []bool{}
	progressDone := make(chan bool)

	mockContext := mocks.NewMockContext(context.Background())
	prepareGenericMocks(mockContext.CommandRunner)
	preparePlanningMocks(mockContext)
	prepareDeployShowMocks(mockContext.HttpClient)
	infraProvider := createBicepProvider(*mockContext.Context)
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

	require.Len(t, progressLog, 2)
	require.Contains(t, progressLog[0], "Generating Bicep parameters file")
	require.Contains(t, progressLog[1], "Compiling Bicep template")

	require.Equal(t, infraProvider.env.Values["AZURE_LOCATION"], deploymentPlan.Deployment.Parameters["location"].Value)
	require.Equal(
		t,
		infraProvider.env.Values["AZURE_ENV_NAME"],
		deploymentPlan.Deployment.Parameters["environmentName"].Value,
	)
}

func TestBicepState(t *testing.T) {
	progressLog := []string{}
	interactiveLog := []bool{}
	progressDone := make(chan bool)
	expectedWebsiteUrl := "http://myapp.azurewebsites.net"

	mockContext := mocks.NewMockContext(context.Background())
	prepareGenericMocks(mockContext.CommandRunner)
	preparePlanningMocks(mockContext)
	prepareDeployShowMocks(mockContext.HttpClient)
	prepareDeployMocks(mockContext.CommandRunner)

	infraProvider := createBicepProvider(*mockContext.Context)
	scope := infra.NewSubscriptionScope(
		*mockContext.Context,
		infraProvider.env.Values["AZURE_LOCATION"],
		infraProvider.env.GetSubscriptionId(),
		infraProvider.env.GetEnvName(),
	)
	getDeploymentTask := infraProvider.State(*mockContext.Context, scope)

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
	require.NotNil(t, getDeploymentResult.State)
	require.Equal(t, getDeploymentResult.State.Outputs["WEBSITE_URL"].Value, expectedWebsiteUrl)

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
	preparePlanningMocks(mockContext)
	prepareDeployShowMocks(mockContext.HttpClient)
	prepareDeployMocks(mockContext.CommandRunner)

	infraProvider := createBicepProvider(*mockContext.Context)
	tmpPath := t.TempDir()
	parametersPath := path.Join(tmpPath, "params.json")
	createTmpFile := os.WriteFile(parametersPath, []byte(testArmParametersFile), osutil.PermissionFile)
	require.NoError(t, createTmpFile)

	deploymentPlan := DeploymentPlan{
		Details: BicepDeploymentDetails{
			ParameterFilePath: parametersPath,
			Template:          to.Ptr(azure.ArmTemplate("{}")),
		},
	}

	scope := infra.NewSubscriptionScope(
		*mockContext.Context,
		infraProvider.env.Values["AZURE_LOCATION"],
		infraProvider.env.GetSubscriptionId(),
		infraProvider.env.GetEnvName(),
	)
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
	require.Equal(t, deployResult.Deployment.Outputs["WEBSITE_URL"].Value, expectedWebsiteUrl)
}

func TestBicepDestroy(t *testing.T) {
	t.Run("Interactive", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		prepareGenericMocks(mockContext.CommandRunner)
		preparePlanningMocks(mockContext)
		prepareDeployShowMocks(mockContext.HttpClient)
		prepareDestroyMocks(mockContext)

		progressLog := []string{}
		interactiveLog := []bool{}
		progressDone := make(chan bool)

		// Setup console mocks
		mockContext.Console.WhenConfirm(func(options input.ConsoleOptions) bool {
			return strings.Contains(options.Message, "This will delete")
		}).Respond(true)

		mockContext.Console.WhenConfirm(func(options input.ConsoleOptions) bool {
			return strings.Contains(
				options.Message,
				"Would you like to permanently delete these Key Vaults/App Configurations",
			)
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
		require.Len(t, consoleOutput, 7)
		require.Contains(t, consoleOutput[0], "This will delete")
		require.Contains(t, consoleOutput[1], "Deleted resource group")
		require.Contains(t, consoleOutput[2], "This operation will delete")
		require.Contains(t, consoleOutput[3], "Would you like to permanently delete these Key Vaults/App Configurations")
		require.Contains(t, consoleOutput[4], "Purged key vault")
		require.Contains(t, consoleOutput[5], "Purged app configuration")
		require.Contains(t, consoleOutput[6], "Deleted deployment")

		// Verify progress output
		require.Len(t, progressLog, 8)
		require.Contains(t, progressLog[0], "Fetching resource groups")
		require.Contains(t, progressLog[1], "Fetching resources")
		require.Contains(t, progressLog[2], "Getting Key Vaults to purge")
		require.Contains(t, progressLog[3], "Getting App Configurations to purge")
		require.Contains(t, progressLog[4], "Deleting resource group")
		require.Contains(t, progressLog[5], "Purging key vault")
		require.Contains(t, progressLog[6], "Purging app configuration")
		require.Contains(t, progressLog[7], "Deleting deployment")
	})

	t.Run("InteractiveForceAndPurge", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		prepareGenericMocks(mockContext.CommandRunner)
		preparePlanningMocks(mockContext)
		prepareDeployShowMocks(mockContext.HttpClient)
		prepareDestroyMocks(mockContext)

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
		require.Len(t, consoleOutput, 2)
		require.Contains(t, consoleOutput[0], "Deleted resource group")
		require.Contains(t, consoleOutput[1], "Deleted deployment")

		// Verify progress output
		require.Len(t, progressLog, 6)
		require.Contains(t, progressLog[0], "Fetching resource groups")
		require.Contains(t, progressLog[1], "Fetching resources")
		require.Contains(t, progressLog[2], "Getting Key Vaults to purge")
		require.Contains(t, progressLog[3], "Getting App Configurations to purge")
		require.Contains(t, progressLog[4], "Deleting resource group")
		require.Contains(t, progressLog[5], "Deleting deployment")
	})
}

func createBicepProvider(ctx context.Context) *BicepProvider {
	projectDir := "../../../../test/functional/testdata/samples/webapp"
	options := Options{
		Module: "main",
	}

	env := environment.EphemeralWithValues("test-env", map[string]string{
		environment.LocationEnvVarName:       "westus2",
		environment.SubscriptionIdEnvVarName: "SUBSCRIPTION_ID",
	})

	return NewBicepProvider(ctx, env, projectDir, options)
}

func prepareGenericMocks(commandRunner *execmock.MockCommandRunner) {
	// Setup expected values for exec
	commandRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(command, "az version")
	}).Respond(exec.RunResult{
		Stdout: `{"azure-cli": "2.38.0"}`,
		Stderr: "",
	})
}

// Sets up all the mocks required for the bicep plan & deploy operation
func prepareDeployMocks(commandRunner *execmock.MockCommandRunner) {
	// Gets deployment progress
	commandRunner.When(
		func(args exec.RunArgs, command string) bool {
			return strings.Contains(command, "az deployment operation sub list")
		}).Respond(exec.RunResult{
		Stdout: "",
		Stderr: "",
	})

	// Gets deployment progress
	commandRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(command, "deployment operation group list")
	}).Respond(exec.RunResult{
		Stdout: "",
		Stderr: "",
	})
}

func preparePlanningMocks(
	mockContext *mocks.MockContext) {
	bicepInputParams := make(map[string]BicepInputParameter)
	bicepInputParams["environmentName"] = BicepInputParameter{Value: "${AZURE_ENV_NAME}"}
	bicepInputParams["location"] = BicepInputParameter{Value: "${AZURE_LOCATION}"}

	bicepOutputParams := make(map[string]BicepOutputParameter)

	bicepTemplate := BicepTemplate{
		Parameters: bicepInputParams,
		Outputs:    bicepOutputParams,
	}

	bicepBytes, _ := json.Marshal(bicepTemplate)
	deployResult := `
	{
		"id":"DEPLOYMENT_ID",
		"name":"DEPLOYMENT_NAME",
		"properties":{
			"outputs":{
				"WEBSITE_URL":{"type": "String", "value": "http://myapp.azurewebsites.net"}
			}
		}
	}`

	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(command, "az bicep build")
	}).Respond(exec.RunResult{
		Stdout: string(bicepBytes),
		Stderr: "",
	})
	mockContext.HttpClient.When(func(request *http.Request) bool {
		return request.Method == http.MethodPut && strings.Contains(
			request.URL.Path,
			"/subscriptions/SUBSCRIPTION_ID/providers/Microsoft.Resources/deployments",
		)
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(bytes.NewBuffer([]byte(deployResult))),
			Request: &http.Request{
				Method: http.MethodGet,
			},
		}, nil
	})
}

func prepareDeployShowMocks(
	httpClient *httputil.MockHttpClient) {
	expectedWebsiteUrl := "http://myapp.azurewebsites.net"

	deployOutputs := make(map[string]interface{})
	deployOutputs["WEBSITE_URL"] = map[string]interface{}{"value": expectedWebsiteUrl, "type": "string"}
	azDeployment := armresources.DeploymentExtended{
		ID:   convert.RefOf("DEPLOYMENT_ID"),
		Name: convert.RefOf("DEPLOYMENT_NAME"),
		Properties: &armresources.DeploymentPropertiesExtended{
			Outputs: deployOutputs,
			Dependencies: []*armresources.Dependency{
				{
					DependsOn: []*armresources.BasicDependency{
						{
							ID:           convert.RefOf("RESOURCE_ID"),
							ResourceName: convert.RefOf("RESOURCE_GROUP"),
							ResourceType: convert.RefOf(string(infra.AzureResourceTypeResourceGroup)),
						},
					},
				},
			},
		},
	}

	deployResultBytes, _ := json.Marshal(azDeployment)

	// Get deployment result
	httpClient.When(func(request *http.Request) bool {
		return request.Method == http.MethodGet && strings.Contains(
			request.URL.Path,
			"/SUBSCRIPTION_ID/providers/Microsoft.Resources/deployments",
		)
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(bytes.NewBuffer(deployResultBytes)),
		}, nil
	})
}

func prepareDestroyMocks(mockContext *mocks.MockContext) {
	resourceList := armresources.ResourceListResult{
		Value: []*armresources.GenericResourceExpanded{
			{
				ID:       convert.RefOf("webapp"),
				Name:     convert.RefOf("app-123"),
				Type:     convert.RefOf(string(infra.AzureResourceTypeWebSite)),
				Location: convert.RefOf("eastus2"),
			},
			{
				ID:       convert.RefOf("keyvault"),
				Name:     convert.RefOf("kv-123"),
				Type:     convert.RefOf(string(infra.AzureResourceTypeKeyVault)),
				Location: convert.RefOf("eastus2"),
			},
			{
				ID:       convert.RefOf("appconfiguration"),
				Name:     convert.RefOf("ac-123"),
				Type:     convert.RefOf(string(infra.AzureResourceTypeAppConfig)),
				Location: convert.RefOf("eastus2"),
			},
		},
	}

	// Get list of resources to delete
	mockContext.HttpClient.When(func(request *http.Request) bool {
		return request.Method == http.MethodGet && strings.Contains(request.URL.Path, "/resources")
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		resourceListBytes, _ := json.Marshal(resourceList)

		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(bytes.NewBuffer(resourceListBytes)),
		}, nil
	})

	// Get Key Vault
	mockContext.HttpClient.When(func(request *http.Request) bool {
		return request.Method == http.MethodGet && strings.Contains(request.URL.Path, "/vaults/kv-123")
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		keyVaultResponse := armkeyvault.VaultsClientGetResponse{
			Vault: armkeyvault.Vault{
				ID:       convert.RefOf("kv-123"),
				Name:     convert.RefOf("kv-123"),
				Location: convert.RefOf("eastus2"),
				Properties: &armkeyvault.VaultProperties{
					EnableSoftDelete:      convert.RefOf(true),
					EnablePurgeProtection: convert.RefOf(false),
				},
			},
		}

		keyVaultBytes, _ := json.Marshal(keyVaultResponse)

		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(bytes.NewBuffer(keyVaultBytes)),
		}, nil
	})

	// Get App Configuration
	mockContext.HttpClient.When(func(request *http.Request) bool {
		return request.Method == http.MethodGet && strings.Contains(request.URL.Path, "/configurationStores/ac-123")
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		appConfigResponse := armappconfiguration.ConfigurationStoresClientGetResponse{
			ConfigurationStore: armappconfiguration.ConfigurationStore{
				ID:       convert.RefOf("ac-123"),
				Name:     convert.RefOf("ac-123"),
				Location: convert.RefOf("eastus2"),
				Properties: &armappconfiguration.ConfigurationStoreProperties{
					EnablePurgeProtection: convert.RefOf(false),
				},
			},
		}

		appConfigBytes, _ := json.Marshal(appConfigResponse)

		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(bytes.NewBuffer(appConfigBytes)),
		}, nil
	})

	// Delete resource group
	mockContext.HttpClient.When(func(request *http.Request) bool {
		return request.Method == http.MethodDelete &&
			strings.Contains(request.URL.Path, "subscriptions/SUBSCRIPTION_ID/resourcegroups/RESOURCE_GROUP")
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		return &http.Response{
			Request:    request,
			Header:     http.Header{},
			StatusCode: http.StatusOK,
			Body:       http.NoBody,
		}, nil
	})

	// Purge Key vault
	mockContext.HttpClient.When(func(request *http.Request) bool {
		return request.Method == http.MethodPost && strings.Contains(request.URL.Path, "deletedVaults/kv-123/purge")
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		return &http.Response{
			Request:    request,
			Header:     http.Header{},
			StatusCode: http.StatusOK,
			Body:       http.NoBody,
		}, nil
	})

	// Purge App configuration
	mockContext.HttpClient.When(func(request *http.Request) bool {
		return request.Method == http.MethodPost && strings.Contains(request.URL.Path,
			"deletedConfigurationStores/ac-123/purge")
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		return &http.Response{
			Request:    request,
			Header:     http.Header{},
			StatusCode: http.StatusOK,
			Body:       http.NoBody,
		}, nil
	})

	// Delete deployment
	mockPollingUrl := "https://url-to-poll.net/keep-deleting"
	mockContext.HttpClient.When(func(request *http.Request) bool {
		return request.Method == http.MethodDelete &&
			strings.Contains(
				request.URL.Path, "/subscriptions/SUBSCRIPTION_ID/providers/Microsoft.Resources/deployments/test-env")
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		response, err := mocks.CreateEmptyHttpResponse(request, 202)
		response.Header.Add("location", mockPollingUrl)
		return response, err
	})
	mockContext.HttpClient.When(func(request *http.Request) bool {
		return request.Method == http.MethodGet &&
			strings.Contains(
				request.URL.String(), mockPollingUrl)
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		// set the end of LRO with 204 response (since we are using location header)
		return mocks.CreateEmptyHttpResponse(request, 204)
	})
}

var testArmParametersFile string = `{
	"parameters": {
		"location": {
			"value": "West US"
		}
	}
}`
