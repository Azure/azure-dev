package mockgraphsdk

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/authorization/armauthorization"
	"github.com/azure/azure-dev/cli/azd/pkg/azsdk"
	"github.com/azure/azure-dev/cli/azd/pkg/convert"
	"github.com/azure/azure-dev/cli/azd/pkg/graphsdk"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
)

func CreateGraphClient(mockContext *mocks.MockContext) (*graphsdk.GraphClient, error) {
	clientOptions := CreateDefaultClientOptions(mockContext)

	return graphsdk.NewGraphClient(mockContext.Credentials, clientOptions)
}

func CreateDefaultClientOptions(mockContext *mocks.MockContext) *azcore.ClientOptions {
	return azsdk.NewClientOptionsBuilder().
		WithTransport(mockContext.HttpClient).
		BuildCoreClientOptions()
}

func RegisterApplicationListMock(mockContext *mocks.MockContext, statusCode int, applications []graphsdk.Application) {
	mockContext.HttpClient.When(func(request *http.Request) bool {
		return request.Method == http.MethodGet && strings.Contains(request.URL.Path, "/applications")
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		listResponse := graphsdk.ApplicationListResponse{
			Value: applications,
		}

		if applications == nil {
			return mocks.CreateEmptyHttpResponse(request, statusCode)
		}

		return mocks.CreateHttpResponseWithBody(request, statusCode, listResponse)
	})
}

func RegisterApplicationGetItemMock(
	mockContext *mocks.MockContext,
	statusCode int,
	appId string,
	application *graphsdk.Application,
) {
	mockContext.HttpClient.When(func(request *http.Request) bool {
		return request.Method == http.MethodGet && strings.Contains(request.URL.Path, fmt.Sprintf("/applications/%s", appId))
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		if application == nil {
			return mocks.CreateEmptyHttpResponse(request, statusCode)
		}

		return mocks.CreateHttpResponseWithBody(request, statusCode, application)
	})
}

func RegisterApplicationDeleteItemMock(
	mockContext *mocks.MockContext,
	appId string,
	statusCode int,
) {
	mockContext.HttpClient.When(func(request *http.Request) bool {
		return request.Method == http.MethodDelete &&
			strings.Contains(request.URL.Path, fmt.Sprintf("/applications/%s", appId))
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		return mocks.CreateEmptyHttpResponse(request, statusCode)
	})
}

func RegisterApplicationCreateItemMock(mockContext *mocks.MockContext, statusCode int, application *graphsdk.Application) {
	mockContext.HttpClient.When(func(request *http.Request) bool {
		return request.Method == http.MethodPost && strings.Contains(request.URL.Path, "/applications")
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		if application == nil {
			return mocks.CreateEmptyHttpResponse(request, statusCode)
		}

		return mocks.CreateHttpResponseWithBody(request, statusCode, application)
	})
}

func RegisterApplicationAddPasswordMock(
	mockContext *mocks.MockContext,
	statusCode int,
	appId string,
	credential *graphsdk.ApplicationPasswordCredential,
) {
	mockContext.HttpClient.When(func(request *http.Request) bool {
		return request.Method == http.MethodPost &&
			strings.Contains(request.URL.Path, fmt.Sprintf("/applications/%s/addPassword", appId))
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		if credential == nil {
			return mocks.CreateEmptyHttpResponse(request, statusCode)
		}

		return mocks.CreateHttpResponseWithBody(request, statusCode, credential)
	})
}

func RegisterApplicationRemovePasswordMock(
	mockContext *mocks.MockContext,
	statusCode int,
	appId string,
) {
	mockContext.HttpClient.When(func(request *http.Request) bool {
		return request.Method == http.MethodPost &&
			strings.Contains(request.URL.Path, fmt.Sprintf("/applications/%s/removePassword", appId))
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		return mocks.CreateHttpResponseWithBody(request, statusCode, map[string]any{})
	})
}

func RegisterServicePrincipalListMock(
	mockContext *mocks.MockContext,
	statusCode int,
	servicePrincipals []graphsdk.ServicePrincipal,
) {
	mockContext.HttpClient.When(func(request *http.Request) bool {
		return request.Method == http.MethodGet && strings.Contains(request.URL.Path, "/servicePrincipals")
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		listResponse := graphsdk.ServicePrincipalListResponse{
			Value: servicePrincipals,
		}

		if servicePrincipals == nil {
			return mocks.CreateEmptyHttpResponse(request, statusCode)
		}

		return mocks.CreateHttpResponseWithBody(request, statusCode, listResponse)
	})
}

func RegisterServicePrincipalGetItemMock(
	mockContext *mocks.MockContext,
	statusCode int,
	spnId string,
	servicePrincipal *graphsdk.ServicePrincipal,
) {
	mockContext.HttpClient.When(func(request *http.Request) bool {
		return request.Method == http.MethodGet &&
			strings.Contains(request.URL.Path, fmt.Sprintf("/servicePrincipals/%s", spnId))
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		if servicePrincipal == nil {
			return mocks.CreateEmptyHttpResponse(request, statusCode)
		}

		return mocks.CreateHttpResponseWithBody(request, statusCode, servicePrincipal)
	})
}

func RegisterServicePrincipalDeleteItemMock(
	mockContext *mocks.MockContext,
	spnId string,
	statusCode int,
) {
	mockContext.HttpClient.When(func(request *http.Request) bool {
		return request.Method == http.MethodDelete &&
			strings.Contains(request.URL.Path, fmt.Sprintf("/servicePrincipals/%s", spnId))
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		return mocks.CreateEmptyHttpResponse(request, statusCode)
	})
}

func RegisterServicePrincipalCreateItemMock(
	mockContext *mocks.MockContext,
	statusCode int,
	servicePrincipal *graphsdk.ServicePrincipal,
) {
	mockContext.HttpClient.When(func(request *http.Request) bool {
		return request.Method == http.MethodPost && strings.Contains(request.URL.Path, "/servicePrincipals")
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		if servicePrincipal == nil {
			return mocks.CreateEmptyHttpResponse(request, statusCode)
		}

		return mocks.CreateHttpResponseWithBody(request, statusCode, servicePrincipal)
	})
}

func RegisterMeGetMock(mockContext *mocks.MockContext, statusCode int, userProfile *graphsdk.UserProfile) {
	mockContext.HttpClient.When(func(request *http.Request) bool {
		return request.Method == http.MethodGet && strings.Contains(request.URL.Path, "/me")
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		if userProfile == nil {
			return mocks.CreateEmptyHttpResponse(request, statusCode)
		}

		return mocks.CreateHttpResponseWithBody(request, statusCode, userProfile)
	})
}

func RegisterRoleDefinitionListMock(
	mockContext *mocks.MockContext,
	statusCode int,
	roleDefinitions []*armauthorization.RoleDefinition,
) {
	mockContext.HttpClient.When(func(request *http.Request) bool {
		return request.Method == http.MethodGet &&
			strings.Contains(request.URL.Path, "/providers/Microsoft.Authorization/roleDefinitions")
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		if roleDefinitions == nil {
			return mocks.CreateEmptyHttpResponse(request, statusCode)
		}

		response := armauthorization.RoleDefinitionsClientListResponse{
			RoleDefinitionListResult: armauthorization.RoleDefinitionListResult{
				Value: roleDefinitions,
			},
		}

		return mocks.CreateHttpResponseWithBody(request, statusCode, response)
	})
}

func RegisterRoleAssignmentPutMock(mockContext *mocks.MockContext, statusCode int) {
	mockContext.HttpClient.When(func(request *http.Request) bool {
		return request.Method == http.MethodPut &&
			strings.Contains(request.URL.Path, "/providers/Microsoft.Authorization/roleAssignments/")
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		response := armauthorization.RoleAssignmentsClientCreateResponse{
			RoleAssignment: armauthorization.RoleAssignment{
				ID:   convert.RefOf("ASSIGNMENT_ID"),
				Name: convert.RefOf("ROLE_NAME"),
				Type: convert.RefOf("ASSIGNMENT_TYPE"),
			},
		}

		if statusCode == http.StatusCreated {
			return mocks.CreateHttpResponseWithBody(request, statusCode, response)
		} else {
			errorBody := map[string]any{
				"error": map[string]any{
					"code":    "RoleAlreadyExists",
					"message": "The role is already assigned",
				},
			}

			return mocks.CreateHttpResponseWithBody(request, statusCode, errorBody)
		}
	})
}

func RegisterFederatedCredentialsListMock(
	mockContext *mocks.MockContext,
	applicationId string,
	statusCode int,
	federatedCredentials []graphsdk.FederatedIdentityCredential,
) {
	mockContext.HttpClient.When(func(request *http.Request) bool {
		return request.Method == http.MethodGet &&
			strings.Contains(request.URL.Path, fmt.Sprintf("/applications/%s/federatedIdentityCredentials", applicationId))
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		listResponse := graphsdk.FederatedIdentityCredentialListResponse{
			Value: federatedCredentials,
		}

		if federatedCredentials == nil {
			return mocks.CreateEmptyHttpResponse(request, statusCode)
		}

		return mocks.CreateHttpResponseWithBody(request, statusCode, listResponse)
	})
}

func RegisterFederatedCredentialCreateItemMock(
	mockContext *mocks.MockContext,
	applicationId string,
	statusCode int,
	federatedCredential *graphsdk.FederatedIdentityCredential,
) {
	mockContext.HttpClient.When(func(request *http.Request) bool {
		return request.Method == http.MethodPost &&
			strings.Contains(request.URL.Path, fmt.Sprintf("/applications/%s", applicationId))
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		if federatedCredential == nil {
			return mocks.CreateEmptyHttpResponse(request, statusCode)
		}

		return mocks.CreateHttpResponseWithBody(request, statusCode, federatedCredential)
	})
}

func RegisterFederatedCredentialPatchItemMock(
	mockContext *mocks.MockContext,
	applicationId string,
	credentialId string,
	statusCode int,
) {
	mockContext.HttpClient.When(func(request *http.Request) bool {
		return request.Method == http.MethodPatch &&
			strings.Contains(
				request.URL.Path,
				fmt.Sprintf("/applications/%s/federatedIdentityCredentials/%s", applicationId, credentialId),
			)
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		return mocks.CreateEmptyHttpResponse(request, statusCode)
	})
}

func RegisterFederatedCredentialGetItemMock(
	mockContext *mocks.MockContext,
	appId string,
	federatedCredentialId string,
	statusCode int,
	federatedCredential *graphsdk.FederatedIdentityCredential,
) {
	mockContext.HttpClient.When(func(request *http.Request) bool {
		return request.Method == http.MethodGet &&
			strings.Contains(
				request.URL.Path,
				fmt.Sprintf("/applications/%s/federatedIdentityCredentials/%s", appId, federatedCredentialId),
			)
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		if federatedCredential == nil {
			return mocks.CreateEmptyHttpResponse(request, statusCode)
		}

		return mocks.CreateHttpResponseWithBody(request, statusCode, federatedCredential)
	})
}

func RegisterFederatedCredentialDeleteItemMock(
	mockContext *mocks.MockContext,
	appId string,
	federatedCredentialId string,
	statusCode int,
) {
	mockContext.HttpClient.When(func(request *http.Request) bool {
		return request.Method == http.MethodDelete &&
			strings.Contains(
				request.URL.Path,
				fmt.Sprintf("/applications/%s/federatedIdentityCredentials/%s", appId, federatedCredentialId),
			)
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		return mocks.CreateEmptyHttpResponse(request, statusCode)
	})
}
