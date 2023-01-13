package azcli

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/authorization/armauthorization"
	"github.com/azure/azure-dev/cli/azd/pkg/convert"
	"github.com/azure/azure-dev/cli/azd/pkg/graphsdk"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockgraphsdk"
	"github.com/stretchr/testify/require"
)

func Test_GetSignedInUserId(t *testing.T) {
	t.Run("Success", func(t *testing.T) {
		mockUserProfile := graphsdk.UserProfile{
			Id:                "user1",
			GivenName:         "John",
			Surname:           "Doe",
			JobTitle:          "Software Engineer",
			DisplayName:       "John Doe",
			UserPrincipalName: "john.doe@contoso.com",
		}

		mockContext := mocks.NewMockContext(context.Background())
		registerGetMeGraphMock(mockContext, http.StatusOK, &mockUserProfile)

		azCli := newAzCliFromMockContext(mockContext)

		userId, err := azCli.GetSignedInUserId(*mockContext.Context)
		require.NoError(t, err)
		require.Equal(t, mockUserProfile.Id, *userId)
	})

	t.Run("Error", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		registerGetMeGraphMock(mockContext, http.StatusBadRequest, nil)

		azCli := newAzCliFromMockContext(mockContext)

		userId, err := azCli.GetSignedInUserId(*mockContext.Context)
		require.Error(t, err)
		require.Nil(t, userId)
	})
}

var expectedServicePrincipalCredential AzureCredentials = AzureCredentials{
	ClientId:                   "CLIENT_ID",
	ClientSecret:               "CLIENT_SECRET",
	SubscriptionId:             "SUBSCRIPTION_ID",
	TenantId:                   "TENANT_ID",
	ResourceManagerEndpointUrl: "https://management.azure.com/",
}

func Test_CreateOrUpdateServicePrincipal(t *testing.T) {
	newApplication := graphsdk.Application{
		Id:          convert.RefOf("UNIQUE_ID"),
		AppId:       &expectedServicePrincipalCredential.ClientId,
		DisplayName: "MY_APP",
	}
	servicePrincipal := graphsdk.ServicePrincipal{
		Id:                     convert.RefOf("SPN_ID"),
		AppId:                  expectedServicePrincipalCredential.ClientId,
		DisplayName:            "SPN_NAME",
		AppOwnerOrganizationId: &expectedServicePrincipalCredential.TenantId,
	}
	credential := &graphsdk.ApplicationPasswordCredential{
		KeyId:       convert.RefOf("KEY_ID"),
		DisplayName: convert.RefOf("Azure Developer CLI"),
		SecretText:  &expectedServicePrincipalCredential.ClientSecret,
	}
	existingApplication := graphsdk.Application{
		Id:          convert.RefOf("UNIQUE_ID"),
		AppId:       &expectedServicePrincipalCredential.ClientId,
		DisplayName: "MY_APP",
		PasswordCredentials: []*graphsdk.ApplicationPasswordCredential{
			credential,
		},
	}
	roleDefinitions := []*armauthorization.RoleDefinition{
		{
			ID:   convert.RefOf("ROLE_ID"),
			Name: convert.RefOf("Contributor"),
			Type: convert.RefOf("ROLE_TYPE"),
		},
	}

	// Tests the use case for a brand new service principal
	t.Run("NewServicePrincipal", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		mockgraphsdk.RegisterApplicationListMock(mockContext, http.StatusOK, []graphsdk.Application{})
		mockgraphsdk.RegisterServicePrincipalListMock(mockContext, http.StatusOK, []graphsdk.ServicePrincipal{})
		mockgraphsdk.RegisterApplicationCreateItemMock(mockContext, http.StatusCreated, &newApplication)
		mockgraphsdk.RegisterServicePrincipalCreateItemMock(mockContext, http.StatusCreated, &servicePrincipal)
		mockgraphsdk.RegisterApplicationAddPasswordMock(mockContext, http.StatusOK, *newApplication.Id, credential)
		mockgraphsdk.RegisterRoleDefinitionListMock(mockContext, http.StatusOK, roleDefinitions)
		mockgraphsdk.RegisterRoleAssignmentPutMock(mockContext, http.StatusCreated)

		azCli := newAzCliFromMockContext(mockContext)
		rawMessage, err := azCli.CreateOrUpdateServicePrincipal(
			*mockContext.Context,
			expectedServicePrincipalCredential.SubscriptionId,
			"APPLICATION_NAME",
			"Contributor",
		)
		require.NoError(t, err)
		require.NotNil(t, rawMessage)

		assertAzureCredentials(t, rawMessage)
	})

	// Tests the use case for updating an existing service principal
	t.Run("ExistingServicePrincipal", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		mockgraphsdk.RegisterApplicationListMock(mockContext, http.StatusOK, []graphsdk.Application{existingApplication})
		mockgraphsdk.RegisterServicePrincipalListMock(
			mockContext,
			http.StatusOK,
			[]graphsdk.ServicePrincipal{servicePrincipal},
		)
		mockgraphsdk.RegisterApplicationRemovePasswordMock(mockContext, http.StatusNoContent, *newApplication.Id)
		mockgraphsdk.RegisterApplicationAddPasswordMock(mockContext, http.StatusOK, *newApplication.Id, credential)
		mockgraphsdk.RegisterRoleDefinitionListMock(mockContext, http.StatusOK, roleDefinitions)
		mockgraphsdk.RegisterRoleAssignmentPutMock(mockContext, http.StatusCreated)

		azCli := newAzCliFromMockContext(mockContext)
		rawMessage, err := azCli.CreateOrUpdateServicePrincipal(
			*mockContext.Context,
			expectedServicePrincipalCredential.SubscriptionId,
			"APPLICATION_NAME",
			"Contributor",
		)
		require.NoError(t, err)
		require.NotNil(t, rawMessage)

		assertAzureCredentials(t, rawMessage)
	})

	// Tests the use case for an existing service principal that already has the required role assignment.
	t.Run("RoleAssignmentExists", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		mockgraphsdk.RegisterApplicationListMock(mockContext, http.StatusOK, []graphsdk.Application{existingApplication})
		mockgraphsdk.RegisterServicePrincipalListMock(
			mockContext,
			http.StatusOK,
			[]graphsdk.ServicePrincipal{servicePrincipal},
		)
		mockgraphsdk.RegisterApplicationRemovePasswordMock(mockContext, http.StatusNoContent, *newApplication.Id)
		mockgraphsdk.RegisterApplicationAddPasswordMock(mockContext, http.StatusOK, *newApplication.Id, credential)
		mockgraphsdk.RegisterRoleDefinitionListMock(mockContext, http.StatusOK, roleDefinitions)
		// Note how role assignment returns a 409 conflict
		mockgraphsdk.RegisterRoleAssignmentPutMock(mockContext, http.StatusConflict)

		azCli := newAzCliFromMockContext(mockContext)
		rawMessage, err := azCli.CreateOrUpdateServicePrincipal(
			*mockContext.Context,
			expectedServicePrincipalCredential.SubscriptionId,
			"APPLICATION_NAME",
			"Contributor",
		)
		require.NoError(t, err)
		require.NotNil(t, rawMessage)

		assertAzureCredentials(t, rawMessage)
	})

	t.Run("InvalidRole", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		mockgraphsdk.RegisterApplicationListMock(mockContext, http.StatusOK, []graphsdk.Application{})
		mockgraphsdk.RegisterServicePrincipalListMock(mockContext, http.StatusOK, []graphsdk.ServicePrincipal{})
		mockgraphsdk.RegisterApplicationCreateItemMock(mockContext, http.StatusCreated, &newApplication)
		mockgraphsdk.RegisterServicePrincipalCreateItemMock(mockContext, http.StatusCreated, &servicePrincipal)
		mockgraphsdk.RegisterApplicationAddPasswordMock(mockContext, http.StatusOK, *newApplication.Id, credential)
		// Note how retrieval of matching role assignments is empty
		mockgraphsdk.RegisterRoleDefinitionListMock(mockContext, http.StatusOK, []*armauthorization.RoleDefinition{})

		azCli := newAzCliFromMockContext(mockContext)
		rawMessage, err := azCli.CreateOrUpdateServicePrincipal(
			*mockContext.Context,
			expectedServicePrincipalCredential.SubscriptionId,
			"APPLICATION_NAME",
			"Contributor",
		)
		require.Error(t, err)
		require.Nil(t, rawMessage)
	})

	t.Run("ErrorCreatingApplication", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		mockgraphsdk.RegisterApplicationListMock(mockContext, http.StatusOK, []graphsdk.Application{})
		mockgraphsdk.RegisterServicePrincipalListMock(mockContext, http.StatusOK, []graphsdk.ServicePrincipal{})
		// Note that the application creation returns an unauthorized error
		mockgraphsdk.RegisterApplicationCreateItemMock(mockContext, http.StatusUnauthorized, nil)

		azCli := newAzCliFromMockContext(mockContext)
		rawMessage, err := azCli.CreateOrUpdateServicePrincipal(
			*mockContext.Context,
			expectedServicePrincipalCredential.SubscriptionId,
			"APPLICATION_NAME",
			"Contributor",
		)
		require.Error(t, err)
		require.Nil(t, rawMessage)
	})
}

func assertAzureCredentials(t *testing.T, message json.RawMessage) {
	jsonBytes, err := message.MarshalJSON()
	require.NoError(t, err)

	var actualCredentials AzureCredentials
	err = json.Unmarshal(jsonBytes, &actualCredentials)
	require.NoError(t, err)
	require.Equal(t, expectedServicePrincipalCredential, actualCredentials)
}

func registerGetMeGraphMock(mockContext *mocks.MockContext, statusCode int, userProfile *graphsdk.UserProfile) {
	mockContext.HttpClient.When(func(request *http.Request) bool {
		return request.Method == http.MethodGet && strings.Contains(request.URL.Path, "/me")
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		if userProfile == nil {
			return mocks.CreateEmptyHttpResponse(request, statusCode)
		}

		return mocks.CreateHttpResponseWithBody(request, statusCode, userProfile)
	})
}
