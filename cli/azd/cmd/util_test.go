package cmd

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockazcli"
	"github.com/stretchr/testify/require"
)

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
