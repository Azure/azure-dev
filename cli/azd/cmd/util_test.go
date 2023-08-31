package cmd

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockazcli"
	"github.com/stretchr/testify/require"
	"golang.org/x/exp/slices"
)

func createEnvManager(t *testing.T, mockContext *mocks.MockContext) environment.Manager {
	azdCtx := azdcontext.NewAzdContextWithDirectory(t.TempDir())
	localDataStore := environment.NewLocalFileDataStore(azdCtx, config.NewFileConfigManager(config.NewManager()))

	return environment.NewManager(azdCtx, mockContext.Console, localDataStore, nil)
}

func Test_promptEnvironmentName(t *testing.T) {
	t.Run("valid name", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		mockContext.Console.WhenConfirm(func(options input.ConsoleOptions) bool {
			return strings.Contains(options.Message, "would you like to create it?")
		}).Respond(true)

		mockContext.Console.WhenPrompt(func(options input.ConsoleOptions) bool {
			return true
		}).SetError(errors.New("prompt should not be called for valid environment name"))

		expected := "hello"
		envManager := createEnvManager(t, mockContext)
		env, err := envManager.CreateInteractive(*mockContext.Context, environment.Spec{Name: expected})
		require.NoError(t, err)
		require.NotNil(t, env)
		require.Equal(t, expected, env.GetEnvName())
	})

	t.Run("empty name gets prompted", func(t *testing.T) {
		expected := "someEnv"

		mockContext := mocks.NewMockContext(context.Background())
		mockContext.Console.WhenConfirm(func(options input.ConsoleOptions) bool {
			return strings.Contains(options.Message, "would you like to create it?")
		}).Respond(true)

		mockContext.Console.WhenPrompt(func(options input.ConsoleOptions) bool {
			return true
		}).Respond(expected)

		envManager := createEnvManager(t, mockContext)
		env, err := envManager.CreateInteractive(*mockContext.Context, environment.Spec{Name: ""})

		require.NoError(t, err)
		require.NotNil(t, env)
		require.Equal(t, expected, env.GetEnvName())
	})
}

func Test_createAndInitEnvironment(t *testing.T) {
	t.Run("invalid name", func(t *testing.T) {
		validEnvName := "validEnvName"
		invalidEnvName := "*!33"
		calls := 0

		mockContext := mocks.NewMockContext(context.Background())
		mockContext.Console.WhenConfirm(func(options input.ConsoleOptions) bool {
			return strings.Contains(options.Message, "would you like to create it?")
		}).Respond(true)

		// Validate that the intial value is invalide
		// Follow-up with another attempt passing a valid name
		mockContext.Console.WhenPrompt(func(options input.ConsoleOptions) bool {
			return strings.Contains(options.Message, "Enter a new environment name")
		}).RespondFn(func(options input.ConsoleOptions) (any, error) {
			calls++
			if calls == 1 {
				return invalidEnvName, nil
			}

			return validEnvName, nil
		})

		// Environment creation should succeed but include a console message with the warning message
		envManager := createEnvManager(t, mockContext)
		env, err := envManager.CreateInteractive(*mockContext.Context, environment.Spec{Name: invalidEnvName})
		require.NoError(t, err)
		require.NotNil(t, env)
		require.Equal(t, validEnvName, env.GetEnvName())

		hasInvalidMessage := slices.ContainsFunc(mockContext.Console.Output(), func(message string) bool {
			return strings.Contains(message, fmt.Sprintf("environment name '%s' is invalid", invalidEnvName))
		})

		require.True(t, hasInvalidMessage)
	})
}

func Test_getResourceGroupFollowUp(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())
	azCli := mockazcli.NewAzCliFromMockContext(mockContext)
	depOpService := mockazcli.NewDeploymentOperationsServiceFromMockContext(mockContext)
	env := environment.NewWithValues("envA", map[string]string{
		environment.SubscriptionIdEnvVarName: "SUBSCRIPTION_ID",
	})

	mockContext.HttpClient.When(func(request *http.Request) bool {
		return request.Method == http.MethodGet && strings.Contains(request.URL.Path, "subscriptions/SUBSCRIPTION_ID/")
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		resp := &armresources.ResourceGroupListResult{
			Value: []*armresources.ResourceGroup{
				{
					Location: to.Ptr("location"),
					ID:       to.Ptr("id"),
					Name:     to.Ptr("Name"),
					Type:     to.Ptr("Type"),
				},
			},
		}
		return mocks.CreateHttpResponseWithBody(request, 200, resp)
	})

	followUp := getResourceGroupFollowUp(
		*mockContext.Context,
		&output.NoneFormatter{},
		&project.ProjectConfig{},
		project.NewResourceManager(env, azCli, depOpService),
		env,
		false)

	require.Contains(t, followUp, "You can view the resources created under the resource group Name in Azure Portal:")
}

func Test_getResourceGroupFollowUpPreview(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())
	azCli := mockazcli.NewAzCliFromMockContext(mockContext)
	depOpService := mockazcli.NewDeploymentOperationsServiceFromMockContext(mockContext)
	env := environment.NewWithValues("envA", map[string]string{
		environment.SubscriptionIdEnvVarName: "SUBSCRIPTION_ID",
	})

	mockContext.HttpClient.When(func(request *http.Request) bool {
		return request.Method == http.MethodGet && strings.Contains(request.URL.Path, "subscriptions/SUBSCRIPTION_ID/")
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		resp := &armresources.ResourceGroupListResult{
			Value: []*armresources.ResourceGroup{
				{
					Location: to.Ptr("location"),
					ID:       to.Ptr("id"),
					Name:     to.Ptr("Name"),
					Type:     to.Ptr("Type"),
				},
			},
		}
		return mocks.CreateHttpResponseWithBody(request, 200, resp)
	})

	followUp := getResourceGroupFollowUp(
		*mockContext.Context,
		&output.NoneFormatter{},
		&project.ProjectConfig{},
		project.NewResourceManager(env, azCli, depOpService),
		env,
		true)

	require.Contains(t, followUp, "You can view the current resources under the resource group Name in Azure Portal:")
}
