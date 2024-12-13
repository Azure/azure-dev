package azapi

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/cognitiveservices/armcognitiveservices"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/stretchr/testify/require"
)

func Test_GetCognitiveAccount(t *testing.T) {
	t.Run("Success", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		azCli := newAzureClientFromMockContext(mockContext)

		expectedName := "ACCOUNT_NAME"
		mockContext.HttpClient.When(func(request *http.Request) bool {
			return request.Method == http.MethodGet &&
				strings.Contains(request.URL.Path, "/Microsoft.CognitiveServices/accounts/ACCOUNT_NAME")
		}).RespondFn(func(request *http.Request) (*http.Response, error) {
			response := armcognitiveservices.Account{
				Name: to.Ptr(expectedName),
			}

			return mocks.CreateHttpResponseWithBody(request, http.StatusOK, response)
		})

		cogAccount, err := azCli.GetCognitiveAccount(
			*mockContext.Context,
			"SUBSCRIPTION_ID",
			"RESOURCE_GROUP_ID",
			"ACCOUNT_NAME",
		)
		require.NoError(t, err)
		require.Equal(t, *cogAccount.Name, expectedName)
	})
}

func Test_PurgeCognitiveAccount(t *testing.T) {
	t.Run("Success", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		azCli := newAzureClientFromMockContext(mockContext)
		mockContext.HttpClient.When(func(request *http.Request) bool {
			return request.Method == http.MethodDelete &&
				strings.Contains(request.URL.Path, "/resourceGroups/RESOURCE_GROUP_ID/deletedAccounts/ACCOUNT_NAME")
		}).RespondFn(func(request *http.Request) (*http.Response, error) {
			response := armcognitiveservices.DeletedAccountsClientPurgeResponse{}
			return mocks.CreateHttpResponseWithBody(request, http.StatusOK, response)
		})

		err := azCli.PurgeCognitiveAccount(
			*mockContext.Context,
			"SUBSCRIPTION_ID",
			"LOCATION",
			"RESOURCE_GROUP_ID",
			"ACCOUNT_NAME",
		)
		require.NoError(t, err)
	})
}
