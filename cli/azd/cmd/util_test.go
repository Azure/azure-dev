package cmd

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockazcli"
	"github.com/stretchr/testify/require"
)

func Test_promptEnvironmentName(t *testing.T) {
	t.Run("valid name", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		mockContext.Console.WhenPrompt(func(options input.ConsoleOptions) bool {
			return true
		}).SetError(errors.New("prompt should not be called for valid environment name"))

		environmentName := "hello"

		err := ensureValidEnvironmentName(*mockContext.Context, &environmentName, nil, mockContext.Console)

		require.NoError(t, err)
	})

	t.Run("empty name gets prompted", func(t *testing.T) {
		environmentName := ""

		mockContext := mocks.NewMockContext(context.Background())
		mockContext.Console.WhenPrompt(func(options input.ConsoleOptions) bool {
			return true
		}).Respond("someEnv")

		err := ensureValidEnvironmentName(*mockContext.Context, &environmentName, nil, mockContext.Console)

		require.NoError(t, err)
		require.Equal(t, "someEnv", environmentName)
	})
}

func Test_createAndInitEnvironment(t *testing.T) {
	t.Run("invalid name", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		tempDir := t.TempDir()
		azdContext := azdcontext.NewAzdContextWithDirectory(tempDir)
		invalidEnvName := "*!33"
		_, err := createEnvironment(
			*mockContext.Context,
			environmentSpec{
				environmentName: invalidEnvName,
			},
			azdContext,
			mockContext.Console,
		)
		require.ErrorContains(
			t,
			err,
			fmt.Sprintf("environment name '%s' is invalid (it should contain only alphanumeric characters and hyphens)\n",
				invalidEnvName))
	})

	t.Run("env already exists", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		tempDir := t.TempDir()
		validName := "azdEnv"
		err := os.MkdirAll(filepath.Join(tempDir, ".azure", validName), 0755)
		require.NoError(t, err)
		azdContext := azdcontext.NewAzdContextWithDirectory(tempDir)

		_, err = createEnvironment(
			*mockContext.Context,
			environmentSpec{
				environmentName: validName,
			},
			azdContext,
			mockContext.Console,
		)
		require.ErrorContains(
			t,
			err,
			fmt.Sprintf("environment '%s' already exists",
				validName))
	})
}

func Test_getResourceGroupFollowUp(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())
	azCli := mockazcli.NewAzCliFromMockContext(mockContext)
	depOpService := mockazcli.NewDeploymentOperationsServiceFromMockContext(mockContext)
	env := environment.EphemeralWithValues("envA", map[string]string{
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
	env := environment.EphemeralWithValues("envA", map[string]string{
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
