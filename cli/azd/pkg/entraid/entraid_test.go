package entraid

import (
	"context"
	"net/http"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/authorization/armauthorization"
	"github.com/azure/azure-dev/cli/azd/pkg/convert"
	"github.com/azure/azure-dev/cli/azd/pkg/graphsdk"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockgraphsdk"
	"github.com/stretchr/testify/require"
)

var defaultRoleNames = []string{"Contributor", "User Access Administrator"}

var expectedServicePrincipalCredential AzureCredentials = AzureCredentials{
	ClientId:       "CLIENT_ID",
	ClientSecret:   "CLIENT_SECRET",
	SubscriptionId: "SUBSCRIPTION_ID",
	TenantId:       "TENANT_ID",
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
		DisplayName: "APPLICATION_NAME",
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
		mockgraphsdk.RegisterApplicationGetItemByAppIdMock(mockContext, http.StatusNotFound, "APPLICATION_NAME", nil)
		mockgraphsdk.RegisterApplicationGetItemMock(mockContext, http.StatusNotFound, "APPLICATION_NAME", nil)
		mockgraphsdk.RegisterServicePrincipalListMock(mockContext, http.StatusOK, []graphsdk.ServicePrincipal{})
		mockgraphsdk.RegisterApplicationCreateItemMock(mockContext, http.StatusCreated, &newApplication)
		mockgraphsdk.RegisterServicePrincipalCreateItemMock(mockContext, http.StatusCreated, &servicePrincipal)
		mockgraphsdk.RegisterApplicationAddPasswordMock(mockContext, http.StatusOK, *newApplication.Id, credential)
		mockgraphsdk.RegisterRoleDefinitionListMock(mockContext, http.StatusOK, roleDefinitions)
		mockgraphsdk.RegisterRoleAssignmentPutMock(mockContext, http.StatusCreated)

		entraIdService := NewEntraIdService(
			mockContext.SubscriptionCredentialProvider,
			mockContext.ArmClientOptions,
			mockContext.CoreClientOptions,
		)
		servicePrincipal, err := entraIdService.CreateOrUpdateServicePrincipal(
			*mockContext.Context,
			expectedServicePrincipalCredential.SubscriptionId,
			"APPLICATION_NAME",
			CreateOrUpdateServicePrincipalOptions{
				RolesToAssign: defaultRoleNames,
			},
		)
		require.NoError(t, err)
		require.NotNil(t, servicePrincipal)
	})

	// Tests the use case for updating an existing service principal
	t.Run("ExistingServicePrincipal", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		mockgraphsdk.RegisterApplicationListMock(
			mockContext,
			http.StatusOK,
			[]graphsdk.Application{existingApplication},
		)
		mockgraphsdk.RegisterApplicationGetItemByAppIdMock(
			mockContext,
			http.StatusOK,
			existingApplication.DisplayName,
			&existingApplication,
		)
		mockgraphsdk.RegisterApplicationGetItemMock(
			mockContext,
			http.StatusOK,
			existingApplication.DisplayName,
			&existingApplication,
		)
		mockgraphsdk.RegisterServicePrincipalListMock(
			mockContext,
			http.StatusOK,
			[]graphsdk.ServicePrincipal{servicePrincipal},
		)
		mockgraphsdk.RegisterApplicationRemovePasswordMock(mockContext, http.StatusNoContent, *newApplication.Id)
		mockgraphsdk.RegisterApplicationAddPasswordMock(mockContext, http.StatusOK, *newApplication.Id, credential)
		mockgraphsdk.RegisterRoleDefinitionListMock(mockContext, http.StatusOK, roleDefinitions)
		mockgraphsdk.RegisterRoleAssignmentPutMock(mockContext, http.StatusCreated)

		entraIdService := NewEntraIdService(
			mockContext.SubscriptionCredentialProvider,
			mockContext.ArmClientOptions,
			mockContext.CoreClientOptions,
		)
		servicePrincipal, err := entraIdService.CreateOrUpdateServicePrincipal(
			*mockContext.Context,
			expectedServicePrincipalCredential.SubscriptionId,
			"APPLICATION_NAME",
			CreateOrUpdateServicePrincipalOptions{
				RolesToAssign: defaultRoleNames,
			},
		)
		require.NoError(t, err)
		require.NotNil(t, servicePrincipal)
	})

	// Tests the use case for an existing service principal that already has the required role assignment.
	t.Run("RoleAssignmentExists", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		mockgraphsdk.RegisterApplicationListMock(mockContext, http.StatusOK, []graphsdk.Application{existingApplication})
		mockgraphsdk.RegisterApplicationGetItemByAppIdMock(
			mockContext,
			http.StatusOK,
			existingApplication.DisplayName,
			&existingApplication,
		)
		mockgraphsdk.RegisterApplicationGetItemMock(
			mockContext,
			http.StatusOK,
			existingApplication.DisplayName,
			&existingApplication,
		)
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

		entraIdService := NewEntraIdService(
			mockContext.SubscriptionCredentialProvider,
			mockContext.ArmClientOptions,
			mockContext.CoreClientOptions,
		)
		servicePrincipal, err := entraIdService.CreateOrUpdateServicePrincipal(
			*mockContext.Context,
			expectedServicePrincipalCredential.SubscriptionId,
			"APPLICATION_NAME",
			CreateOrUpdateServicePrincipalOptions{
				RolesToAssign: defaultRoleNames,
			},
		)
		require.NoError(t, err)
		require.NotNil(t, servicePrincipal)
	})

	t.Run("InvalidRole", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		mockgraphsdk.RegisterApplicationListMock(mockContext, http.StatusOK, []graphsdk.Application{})
		mockgraphsdk.RegisterApplicationGetItemByAppIdMock(
			mockContext,
			http.StatusOK,
			existingApplication.DisplayName,
			&existingApplication,
		)
		mockgraphsdk.RegisterApplicationGetItemMock(
			mockContext,
			http.StatusOK,
			existingApplication.DisplayName,
			&existingApplication,
		)
		mockgraphsdk.RegisterServicePrincipalListMock(mockContext, http.StatusOK, []graphsdk.ServicePrincipal{})
		mockgraphsdk.RegisterApplicationCreateItemMock(mockContext, http.StatusCreated, &newApplication)
		mockgraphsdk.RegisterServicePrincipalCreateItemMock(mockContext, http.StatusCreated, &servicePrincipal)
		mockgraphsdk.RegisterApplicationAddPasswordMock(mockContext, http.StatusOK, *newApplication.Id, credential)
		// Note how retrieval of matching role assignments is empty
		mockgraphsdk.RegisterRoleDefinitionListMock(mockContext, http.StatusOK, []*armauthorization.RoleDefinition{})

		entraIdService := NewEntraIdService(
			mockContext.SubscriptionCredentialProvider,
			mockContext.ArmClientOptions,
			mockContext.CoreClientOptions,
		)
		servicePrincipal, err := entraIdService.CreateOrUpdateServicePrincipal(
			*mockContext.Context,
			expectedServicePrincipalCredential.SubscriptionId,
			"APPLICATION_NAME",
			CreateOrUpdateServicePrincipalOptions{
				RolesToAssign: defaultRoleNames,
			},
		)
		require.Error(t, err)
		require.Nil(t, servicePrincipal)
	})

	t.Run("ErrorCreatingApplication", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		mockgraphsdk.RegisterApplicationListMock(mockContext, http.StatusOK, []graphsdk.Application{})
		mockgraphsdk.RegisterApplicationGetItemByAppIdMock(mockContext, http.StatusNotFound, "APPLICATION_NAME", nil)
		mockgraphsdk.RegisterApplicationGetItemMock(mockContext, http.StatusNotFound, "APPLICATION_NAME", nil)
		mockgraphsdk.RegisterServicePrincipalListMock(mockContext, http.StatusOK, []graphsdk.ServicePrincipal{})
		// Note that the application creation returns an unauthorized error
		mockgraphsdk.RegisterApplicationCreateItemMock(mockContext, http.StatusUnauthorized, nil)

		entraIdService := NewEntraIdService(
			mockContext.SubscriptionCredentialProvider,
			mockContext.ArmClientOptions,
			mockContext.CoreClientOptions,
		)
		servicePrincipal, err := entraIdService.CreateOrUpdateServicePrincipal(
			*mockContext.Context,
			expectedServicePrincipalCredential.SubscriptionId,
			"APPLICATION_NAME",
			CreateOrUpdateServicePrincipalOptions{
				RolesToAssign: defaultRoleNames,
			},
		)
		require.Error(t, err)
		require.Nil(t, servicePrincipal)
	})
}

func Test_ApplyFederatedCredentials(t *testing.T) {
	mockApplication := &graphsdk.Application{
		Id:                  convert.RefOf("APPLICATION_ID"),
		AppId:               convert.RefOf("CLIENT_ID"),
		DisplayName:         "APPLICATION_NAME",
		Description:         convert.RefOf("DESCRIPTION"),
		PasswordCredentials: []*graphsdk.ApplicationPasswordCredential{},
	}

	t.Run("AppNotFound", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		mockgraphsdk.RegisterApplicationGetItemByAppIdMock(mockContext, http.StatusNotFound, *mockApplication.AppId, nil)
		entraIdService := NewEntraIdService(
			mockContext.SubscriptionCredentialProvider,
			mockContext.ArmClientOptions,
			mockContext.CoreClientOptions,
		)

		credentials, err := entraIdService.ApplyFederatedCredentials(
			*mockContext.Context,
			"SUBSCRIPTION_ID",
			*mockApplication.AppId,
			nil,
		)

		require.Error(t, err)
		require.ErrorContains(t, err, "failed finding matching application")
		require.Nil(t, credentials)
	})
	t.Run("SingleBranch", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		mockCredentials := []graphsdk.FederatedIdentityCredential{
			{
				Id:          convert.RefOf("CREDENTIAL_ID"),
				Name:        "owner-repo-pull_request",
				Issuer:      federatedIdentityIssuer,
				Subject:     "repo:owner/repo:pull_request",
				Description: convert.RefOf("DESCRIPTION"),
				Audiences:   []string{federatedIdentityAudience},
			},
			{
				Id:          convert.RefOf("CREDENTIAL_ID"),
				Name:        "owner-repo-main",
				Issuer:      federatedIdentityIssuer,
				Subject:     "repo:owner/repo:ref:refs/heads/main",
				Description: convert.RefOf("DESCRIPTION"),
				Audiences:   []string{federatedIdentityAudience},
			},
		}

		mockgraphsdk.RegisterApplicationGetItemByAppIdMock(
			mockContext,
			http.StatusOK,
			*mockApplication.AppId,
			mockApplication,
		)
		mockgraphsdk.RegisterFederatedCredentialsListMock(
			mockContext,
			*mockApplication.Id,
			http.StatusOK,
			[]graphsdk.FederatedIdentityCredential{},
		)
		mockgraphsdk.RegisterFederatedCredentialCreateItemMock(
			mockContext,
			*mockApplication.Id,
			http.StatusCreated,
			&graphsdk.FederatedIdentityCredential{},
		)
		entraIdService := NewEntraIdService(
			mockContext.SubscriptionCredentialProvider,
			mockContext.ArmClientOptions,
			mockContext.CoreClientOptions,
		)

		credentials, err := entraIdService.ApplyFederatedCredentials(
			*mockContext.Context,
			"SUBSCRIPTION_ID",
			*mockApplication.AppId,
			[]*graphsdk.FederatedIdentityCredential{&mockCredentials[0], &mockCredentials[1]},
		)

		require.NoError(t, err)
		require.NotNil(t, credentials)
		require.Len(t, credentials, 2)
	})

	t.Run("MultipleBranches", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		mockCredentials := []graphsdk.FederatedIdentityCredential{
			{
				Id:          convert.RefOf("CREDENTIAL_ID"),
				Name:        "owner-repo-pull_request",
				Issuer:      federatedIdentityIssuer,
				Subject:     "repo:owner/repo:pull_request",
				Description: convert.RefOf("DESCRIPTION"),
				Audiences:   []string{federatedIdentityAudience},
			},
			{
				Id:          convert.RefOf("CREDENTIAL_ID"),
				Name:        "owner-repo-main",
				Issuer:      federatedIdentityIssuer,
				Subject:     "repo:owner/repo:ref:refs/heads/main",
				Description: convert.RefOf("DESCRIPTION"),
				Audiences:   []string{federatedIdentityAudience},
			},
			{
				Id:          convert.RefOf("CREDENTIAL_ID"),
				Name:        "owner-repo-dev",
				Issuer:      federatedIdentityIssuer,
				Subject:     "repo:owner/repo:ref:refs/heads/dev",
				Description: convert.RefOf("DESCRIPTION"),
				Audiences:   []string{federatedIdentityAudience},
			},
		}

		mockgraphsdk.RegisterApplicationGetItemByAppIdMock(
			mockContext,
			http.StatusOK,
			*mockApplication.AppId,
			mockApplication,
		)
		mockgraphsdk.RegisterFederatedCredentialsListMock(
			mockContext,
			*mockApplication.Id,
			http.StatusOK,
			[]graphsdk.FederatedIdentityCredential{},
		)
		mockgraphsdk.RegisterFederatedCredentialCreateItemMock(
			mockContext,
			*mockApplication.Id,
			http.StatusCreated,
			&graphsdk.FederatedIdentityCredential{},
		)
		entraIdService := NewEntraIdService(
			mockContext.SubscriptionCredentialProvider,
			mockContext.ArmClientOptions,
			mockContext.CoreClientOptions,
		)

		credentials, err := entraIdService.ApplyFederatedCredentials(
			*mockContext.Context,
			"SUBSCRIPTION_ID",
			*mockApplication.AppId,
			[]*graphsdk.FederatedIdentityCredential{&mockCredentials[0], &mockCredentials[1], &mockCredentials[2]},
		)

		require.NoError(t, err)
		require.NotNil(t, credentials)
		require.Len(t, credentials, 3)
	})

	t.Run("CredentialsAlreadyExist", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		mockCredentials := []graphsdk.FederatedIdentityCredential{
			{
				Id:          convert.RefOf("CREDENTIAL_ID"),
				Name:        "owner-repo-pull_request",
				Issuer:      federatedIdentityIssuer,
				Subject:     "repo:owner/repo:pull_request",
				Description: convert.RefOf("DESCRIPTION"),
				Audiences:   []string{federatedIdentityAudience},
			},
			{
				Id:          convert.RefOf("CREDENTIAL_ID"),
				Name:        "owner-repo-main",
				Issuer:      federatedIdentityIssuer,
				Subject:     "repo:owner/repo:ref:refs/heads/main",
				Description: convert.RefOf("DESCRIPTION"),
				Audiences:   []string{federatedIdentityAudience},
			},
		}

		mockgraphsdk.RegisterApplicationGetItemByAppIdMock(
			mockContext,
			http.StatusOK,
			*mockApplication.AppId,
			mockApplication,
		)
		mockgraphsdk.RegisterFederatedCredentialsListMock(mockContext, *mockApplication.Id, http.StatusOK, mockCredentials)
		entraIdService := NewEntraIdService(
			mockContext.SubscriptionCredentialProvider,
			mockContext.ArmClientOptions,
			mockContext.CoreClientOptions,
		)

		credentials, err := entraIdService.ApplyFederatedCredentials(
			*mockContext.Context,
			"SUBSCRIPTION_ID",
			*mockApplication.AppId,
			[]*graphsdk.FederatedIdentityCredential{&mockCredentials[0], &mockCredentials[1]},
		)

		require.NoError(t, err)
		require.NotNil(t, credentials)
		// No new credentials should be created
		require.Len(t, credentials, 0)
	})
}

func Test_ResetPasswordCredentials(t *testing.T) {
	mockApplicationPassword := &graphsdk.ApplicationPasswordCredential{
		KeyId:       convert.RefOf("KEY_ID"),
		DisplayName: convert.RefOf("KEY NAME"),
		SecretText:  convert.RefOf("CLIENT_SECRET"),
	}

	mockApplication := &graphsdk.Application{
		Id:                  convert.RefOf("APPLICATION_ID"),
		AppId:               convert.RefOf("CLIENT_ID"),
		DisplayName:         "APPLICATION_NAME",
		Description:         convert.RefOf("DESCRIPTION"),
		PasswordCredentials: []*graphsdk.ApplicationPasswordCredential{mockApplicationPassword},
	}

	mockServicePrincipals := []graphsdk.ServicePrincipal{
		{
			Id:                     convert.RefOf("SPN_ID"),
			AppId:                  *mockApplication.AppId,
			DisplayName:            mockApplication.DisplayName,
			AppOwnerOrganizationId: convert.RefOf("TENANT_ID"),
			AppDisplayName:         &mockApplication.DisplayName,
			Description:            mockApplication.Description,
		},
	}

	t.Run("Success", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		mockgraphsdk.RegisterApplicationGetItemByAppIdMock(
			mockContext,
			http.StatusOK,
			*mockApplication.AppId,
			mockApplication,
		)
		mockgraphsdk.RegisterServicePrincipalListMock(mockContext, http.StatusOK, mockServicePrincipals)
		mockgraphsdk.RegisterApplicationRemovePasswordMock(mockContext, http.StatusNoContent, *mockApplication.Id)
		mockgraphsdk.RegisterApplicationAddPasswordMock(
			mockContext,
			http.StatusOK,
			*mockApplication.Id,
			mockApplicationPassword,
		)

		entraIdService := NewEntraIdService(
			mockContext.SubscriptionCredentialProvider,
			mockContext.ArmClientOptions,
			mockContext.CoreClientOptions,
		)
		credentials, err := entraIdService.ResetPasswordCredentials(
			*mockContext.Context,
			"SUBSCRIPTION_ID",
			*mockApplication.AppId,
		)
		require.NoError(t, err)
		require.NotNil(t, credentials)
		require.Equal(t, *mockApplicationPassword.SecretText, credentials.ClientSecret)
		require.Equal(t, *mockApplication.AppId, credentials.ClientId)
		require.Equal(t, *mockServicePrincipals[0].AppOwnerOrganizationId, credentials.TenantId)
		require.Equal(t, "SUBSCRIPTION_ID", credentials.SubscriptionId)
	})

	t.Run("AppNotFound", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		mockgraphsdk.RegisterApplicationGetItemByAppIdMock(mockContext, http.StatusOK, *mockApplication.AppId, nil)

		entraIdService := NewEntraIdService(
			mockContext.SubscriptionCredentialProvider,
			mockContext.ArmClientOptions,
			mockContext.CoreClientOptions,
		)
		credentials, err := entraIdService.ResetPasswordCredentials(
			*mockContext.Context,
			"SUBSCRIPTION_ID",
			*mockApplication.AppId,
		)
		require.Error(t, err)
		require.ErrorContains(t, err, "failed finding matching application")
		require.Nil(t, credentials)
	})

	t.Run("RemovingOldPassword", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		mockgraphsdk.RegisterApplicationGetItemByAppIdMock(
			mockContext,
			http.StatusOK,
			*mockApplication.AppId,
			mockApplication,
		)
		mockgraphsdk.RegisterServicePrincipalListMock(mockContext, http.StatusOK, mockServicePrincipals)
		mockgraphsdk.RegisterApplicationRemovePasswordMock(mockContext, http.StatusBadRequest, *mockApplication.Id)

		entraIdService := NewEntraIdService(
			mockContext.SubscriptionCredentialProvider,
			mockContext.ArmClientOptions,
			mockContext.CoreClientOptions,
		)
		credentials, err := entraIdService.ResetPasswordCredentials(
			*mockContext.Context,
			"SUBSCRIPTION_ID",
			*mockApplication.AppId,
		)
		require.Error(t, err)
		require.ErrorContains(t, err, "failed removing credentials")
		require.Nil(t, credentials)
	})

	t.Run("AddingNewPassword", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		mockgraphsdk.RegisterApplicationGetItemByAppIdMock(
			mockContext,
			http.StatusOK,
			*mockApplication.AppId,
			mockApplication,
		)
		mockgraphsdk.RegisterServicePrincipalListMock(mockContext, http.StatusOK, mockServicePrincipals)
		mockgraphsdk.RegisterApplicationRemovePasswordMock(mockContext, http.StatusNoContent, *mockApplication.Id)
		mockgraphsdk.RegisterApplicationAddPasswordMock(mockContext, http.StatusBadRequest, *mockApplication.Id, nil)

		entraIdService := NewEntraIdService(
			mockContext.SubscriptionCredentialProvider,
			mockContext.ArmClientOptions,
			mockContext.CoreClientOptions,
		)
		credentials, err := entraIdService.ResetPasswordCredentials(
			*mockContext.Context,
			"SUBSCRIPTION_ID",
			*mockApplication.AppId,
		)
		require.Error(t, err)
		require.ErrorContains(t, err, "failed adding new password credential")
		require.Nil(t, credentials)
	})
}
