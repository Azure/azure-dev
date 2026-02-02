// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package terraform

import (
	"context"
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/account"
	"github.com/azure/azure-dev/cli/azd/pkg/azapi"
	"github.com/azure/azure-dev/cli/azd/pkg/cloud"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning"
	"github.com/azure/azure-dev/cli/azd/pkg/prompt"
	terraformTools "github.com/azure/azure-dev/cli/azd/pkg/tools/terraform"
	"github.com/azure/azure-dev/cli/azd/test/mocks"

	"github.com/azure/azure-dev/cli/azd/test/mocks/mockaccount"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockenv"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockexec"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestTerraformPlan(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())
	prepareGenericMocks(mockContext.CommandRunner)
	preparePlanningMocks(mockContext.CommandRunner)

	infraProvider := createTerraformProvider(t, mockContext)
	deployment, deploymentPlan, err := infraProvider.plan(*mockContext.Context)

	require.Nil(t, err)
	require.NotNil(t, deployment)

	consoleLog := mockContext.Console.Output()

	require.Len(t, consoleLog, 0)

	require.Equal(t, infraProvider.env.Dotenv()["AZURE_LOCATION"], deployment.Parameters["location"].Value)
	require.Equal(
		t,
		infraProvider.env.Dotenv()["AZURE_ENV_NAME"],
		deployment.Parameters["environment_name"].Value,
	)

	require.NotNil(t, deploymentPlan)

	require.NotNil(t, deploymentPlan)

	require.FileExists(t, deploymentPlan.ParameterFilePath)
	require.NotEmpty(t, deploymentPlan.ParameterFilePath)
	require.NotEmpty(t, deploymentPlan.localStateFilePath)
}

func TestTerraformDestroy(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())
	prepareGenericMocks(mockContext.CommandRunner)
	preparePlanningMocks(mockContext.CommandRunner)
	prepareDestroyMocks(mockContext.CommandRunner)

	infraProvider := createTerraformProvider(t, mockContext)
	destroyOptions := provisioning.NewDestroyOptions(false, false)
	destroyResult, err := infraProvider.Destroy(*mockContext.Context, destroyOptions)

	require.Nil(t, err)
	require.NotNil(t, destroyResult)

	require.Contains(t, destroyResult.InvalidatedEnvKeys, "AZURE_LOCATION")
	require.Contains(t, destroyResult.InvalidatedEnvKeys, "RG_NAME")
}

func TestTerraformState(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())
	prepareGenericMocks(mockContext.CommandRunner)
	prepareShowMocks(mockContext.CommandRunner)

	infraProvider := createTerraformProvider(t, mockContext)
	getStateResult, err := infraProvider.State(*mockContext.Context, nil)

	require.Nil(t, err)
	require.NotNil(t, getStateResult.State)

	require.Equal(t, infraProvider.env.Dotenv()["AZURE_LOCATION"], getStateResult.State.Outputs["AZURE_LOCATION"].Value)
	require.Equal(t, fmt.Sprintf("rg-%s", infraProvider.env.Name()), getStateResult.State.Outputs["RG_NAME"].Value)
	require.Len(t, getStateResult.State.Resources, 1)
	require.Regexp(
		t,
		regexp.MustCompile(`^/subscriptions/[^/]*/resourceGroups/[^/]*$`),
		getStateResult.State.Resources[0].Id,
	)
}

func createTerraformProvider(t *testing.T, mockContext *mocks.MockContext) *TerraformProvider {
	projectDir := "../../../../test/functional/testdata/samples/resourcegroupterraform"
	options := provisioning.Options{
		Module: "main",
	}

	env := environment.NewWithValues("test-env", map[string]string{
		"AZURE_LOCATION":        "westus2",
		"AZURE_SUBSCRIPTION_ID": "00000000-0000-0000-0000-000000000000",
	})

	resourceService := azapi.NewResourceService(mockContext.SubscriptionCredentialProvider, mockContext.ArmClientOptions)
	accountManager := &mockaccount.MockAccountManager{
		Subscriptions: []account.Subscription{
			{
				Id:   "00000000-0000-0000-0000-000000000000",
				Name: "test",
			},
		},
		Locations: []account.Location{
			{
				Name:                "location",
				DisplayName:         "Test Location",
				RegionalDisplayName: "(US) Test Location",
			},
		},
	}

	envManager := &mockenv.MockEnvManager{}
	envManager.On("Save", mock.Anything, mock.Anything).Return(nil)

	provider := NewTerraformProvider(
		terraformTools.NewCli(mockContext.CommandRunner),
		envManager,
		env,
		mockContext.Console,
		&mockCurrentPrincipal{},
		prompt.NewDefaultPrompter(env, mockContext.Console, accountManager, resourceService, cloud.AzurePublic()),
	)

	err := provider.Initialize(*mockContext.Context, projectDir, options)
	require.NoError(t, err)

	return provider.(*TerraformProvider)
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

func (m *mockCurrentPrincipal) CurrentPrincipalType(_ context.Context) (provisioning.PrincipalType, error) {
	return provisioning.UserType, nil
}

func TestIsRemoteBackendConfig(t *testing.T) {
	tests := []struct {
		name           string
		backendFile    string
		expectedRemote bool
	}{
		{
			name:           "azurerm backend",
			backendFile:    "azurerm.tf",
			expectedRemote: true,
		},
		{
			name:           "remote backend (Terraform Cloud legacy)",
			backendFile:    "remote.tf",
			expectedRemote: true,
		},
		{
			name:           "cloud block (Terraform Cloud new syntax)",
			backendFile:    "cloud.tf",
			expectedRemote: true,
		},
		{
			name:           "s3 backend",
			backendFile:    "s3.tf",
			expectedRemote: true,
		},
		{
			name:           "gcs backend",
			backendFile:    "gcs.tf",
			expectedRemote: true,
		},
		{
			name:           "local backend",
			backendFile:    "local.tf",
			expectedRemote: false,
		},
		{
			name:           "no backend specified",
			backendFile:    "no_backend.tf",
			expectedRemote: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockContext := mocks.NewMockContext(context.Background())
			prepareGenericMocks(mockContext.CommandRunner)

			// Create a temporary directory for the test
			tmpDir := t.TempDir()
			infraDir := filepath.Join(tmpDir, "infra")
			err := os.MkdirAll(infraDir, 0755)
			require.NoError(t, err)

			// Copy the test backend file to the temporary infra directory
			testDataPath := filepath.Join("testdata", "backend_tests", tt.backendFile)
			testContent, err := os.ReadFile(testDataPath)
			require.NoError(t, err)

			err = os.WriteFile(filepath.Join(infraDir, "main.tf"), testContent, 0600)
			require.NoError(t, err)

			// Create a TerraformProvider instance
			options := provisioning.Options{
				Module: "main",
			}

			env := environment.NewWithValues("test-env", map[string]string{
				"AZURE_LOCATION":        "westus2",
				"AZURE_SUBSCRIPTION_ID": "00000000-0000-0000-0000-000000000000",
			})

			resourceService := azapi.NewResourceService(
				mockContext.SubscriptionCredentialProvider,
				mockContext.ArmClientOptions,
			)
			accountManager := &mockaccount.MockAccountManager{
				Subscriptions: []account.Subscription{
					{
						Id:   "00000000-0000-0000-0000-000000000000",
						Name: "test",
					},
				},
				Locations: []account.Location{
					{
						Name:                "location",
						DisplayName:         "Test Location",
						RegionalDisplayName: "(US) Test Location",
					},
				},
			}

			envManager := &mockenv.MockEnvManager{}
			envManager.On("Save", mock.Anything, mock.Anything).Return(nil)

			provider := NewTerraformProvider(
				terraformTools.NewCli(mockContext.CommandRunner),
				envManager,
				env,
				mockContext.Console,
				&mockCurrentPrincipal{},
				prompt.NewDefaultPrompter(
					env,
					mockContext.Console,
					accountManager,
					resourceService,
					cloud.AzurePublic(),
				),
			)

			err = provider.Initialize(*mockContext.Context, tmpDir, options)
			require.NoError(t, err)

			tfProvider := provider.(*TerraformProvider)

			// Test the isRemoteBackendConfig function
			isRemote, err := tfProvider.isRemoteBackendConfig()
			require.NoError(t, err)
			require.Equal(t, tt.expectedRemote, isRemote, "Expected isRemote=%v for %s", tt.expectedRemote, tt.name)
		})
	}
}
