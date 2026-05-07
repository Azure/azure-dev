// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package armmsi

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/msi/armmsi"
	"github.com/stretchr/testify/require"

	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockaccount"
)

const (
	testSubId    = "SUBSCRIPTION_ID"
	testRgName   = "RESOURCE_GROUP"
	testMsiName  = "MSI_NAME"
	testMsiResId = "/subscriptions/SUBSCRIPTION_ID/resourceGroups/" +
		"RESOURCE_GROUP/providers/" +
		"Microsoft.ManagedIdentity/userAssignedIdentities/MSI_NAME"
)

func TestNewArmMsiService(t *testing.T) {
	mockCtx := mocks.NewMockContext(t.Context())
	svc := NewArmMsiService(
		mockCtx.SubscriptionCredentialProvider,
		mockCtx.ArmClientOptions,
	)
	require.NotNil(t, svc.credentialProvider)
}

func TestGetUserIdentity_InvalidResourceId(t *testing.T) {
	mockCtx := mocks.NewMockContext(t.Context())
	svc := NewArmMsiService(
		mockCtx.SubscriptionCredentialProvider,
		mockCtx.ArmClientOptions,
	)

	_, err := svc.GetUserIdentity(t.Context(), "not-a-resource-id")
	require.Error(t, err)
	require.Contains(t, err.Error(), "parsing MSI resource id")
}

func TestGetUserIdentity_CredentialError(t *testing.T) {
	expectedErr := errors.New("credential failure")
	credProvider := mockaccount.SubscriptionCredentialProviderFunc(
		func(_ context.Context, _ string) (azcore.TokenCredential, error) {
			return nil, expectedErr
		},
	)

	svc := NewArmMsiService(credProvider, nil)

	_, err := svc.GetUserIdentity(t.Context(), testMsiResId)
	require.ErrorIs(t, err, expectedErr)
}

func TestGetUserIdentity_Success(t *testing.T) {
	mockCtx := mocks.NewMockContext(t.Context())

	mockCtx.HttpClient.When(func(request *http.Request) bool {
		return request.Method == http.MethodGet &&
			strings.Contains(
				request.URL.Path,
				"Microsoft.ManagedIdentity/userAssignedIdentities",
			)
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		body := armmsi.Identity{
			ID:       new(testMsiResId),
			Name:     new(testMsiName),
			Location: new("eastus2"),
		}
		return mocks.CreateHttpResponseWithBody(
			request, http.StatusOK, body,
		)
	})

	svc := NewArmMsiService(
		mockCtx.SubscriptionCredentialProvider,
		mockCtx.ArmClientOptions,
	)

	identity, err := svc.GetUserIdentity(t.Context(), testMsiResId)
	require.NoError(t, err)
	require.Equal(t, testMsiName, *identity.Name)
	require.Equal(t, "eastus2", *identity.Location)
}

func TestListUserIdentities_CredentialError(t *testing.T) {
	expectedErr := errors.New("credential failure")
	credProvider := mockaccount.SubscriptionCredentialProviderFunc(
		func(_ context.Context, _ string) (azcore.TokenCredential, error) {
			return nil, expectedErr
		},
	)

	svc := NewArmMsiService(credProvider, nil)

	_, err := svc.ListUserIdentities(t.Context(), testSubId)
	require.ErrorIs(t, err, expectedErr)
}

func TestListUserIdentities_Success(t *testing.T) {
	mockCtx := mocks.NewMockContext(t.Context())

	mockCtx.HttpClient.When(func(request *http.Request) bool {
		return request.Method == http.MethodGet &&
			strings.Contains(
				request.URL.Path,
				"Microsoft.ManagedIdentity/userAssignedIdentities",
			)
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		body := armmsi.UserAssignedIdentitiesListResult{
			Value: []*armmsi.Identity{
				{
					Name:     new("msi-one"),
					Location: new("eastus2"),
				},
				{
					Name:     new("msi-two"),
					Location: new("westus"),
				},
			},
		}
		return mocks.CreateHttpResponseWithBody(
			request, http.StatusOK, body,
		)
	})

	svc := NewArmMsiService(
		mockCtx.SubscriptionCredentialProvider,
		mockCtx.ArmClientOptions,
	)

	identities, err := svc.ListUserIdentities(t.Context(), testSubId)
	require.NoError(t, err)
	require.Len(t, identities, 2)
	require.Equal(t, "msi-one", *identities[0].Name)
	require.Equal(t, "msi-two", *identities[1].Name)
}

func TestListUserIdentities_HandlesNilEntries(t *testing.T) {
	mockCtx := mocks.NewMockContext(t.Context())

	mockCtx.HttpClient.When(func(request *http.Request) bool {
		return request.Method == http.MethodGet &&
			strings.Contains(
				request.URL.Path,
				"Microsoft.ManagedIdentity/userAssignedIdentities",
			)
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		body := armmsi.UserAssignedIdentitiesListResult{
			Value: []*armmsi.Identity{
				{Name: new("msi-one")},
				nil,
				{Name: new("msi-three")},
			},
		}
		return mocks.CreateHttpResponseWithBody(
			request, http.StatusOK, body,
		)
	})

	svc := NewArmMsiService(
		mockCtx.SubscriptionCredentialProvider,
		mockCtx.ArmClientOptions,
	)

	identities, err := svc.ListUserIdentities(t.Context(), testSubId)
	require.NoError(t, err)
	// Result slice has same length; nil entries become zero-value
	require.Len(t, identities, 3)
	require.Equal(t, "msi-one", *identities[0].Name)
	require.Nil(t, identities[1].Name)
	require.Equal(t, "msi-three", *identities[2].Name)
}

func TestCreateFederatedCredential_CredentialError(t *testing.T) {
	expectedErr := errors.New("credential failure")
	credProvider := mockaccount.SubscriptionCredentialProviderFunc(
		func(_ context.Context, _ string) (azcore.TokenCredential, error) {
			return nil, expectedErr
		},
	)

	svc := NewArmMsiService(credProvider, nil)

	_, err := svc.CreateFederatedCredential(
		t.Context(),
		testSubId, testRgName, testMsiName,
		"cred-name", "subject", "https://issuer",
		[]string{"api://AzureADTokenExchange"},
	)
	require.ErrorIs(t, err, expectedErr)
}

func TestCreateFederatedCredential_Success(t *testing.T) {
	mockCtx := mocks.NewMockContext(t.Context())

	mockCtx.HttpClient.When(func(request *http.Request) bool {
		return request.Method == http.MethodPut &&
			strings.Contains(
				request.URL.Path,
				"federatedIdentityCredentials",
			)
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		body := armmsi.FederatedIdentityCredential{
			Name: new("my-cred"),
			Properties: &armmsi.FederatedIdentityCredentialProperties{
				Subject:   new("my-subject"),
				Issuer:    new("https://issuer"),
				Audiences: []*string{new("aud1")},
			},
		}
		return mocks.CreateHttpResponseWithBody(
			request, http.StatusOK, body,
		)
	})

	svc := NewArmMsiService(
		mockCtx.SubscriptionCredentialProvider,
		mockCtx.ArmClientOptions,
	)

	cred, err := svc.CreateFederatedCredential(
		t.Context(),
		testSubId, testRgName, testMsiName,
		"my-cred", "my-subject", "https://issuer",
		[]string{"aud1"},
	)
	require.NoError(t, err)
	require.Equal(t, "my-cred", *cred.Name)
	require.Equal(t, "my-subject", *cred.Properties.Subject)
}

func TestApplyFederatedCredentials_InvalidResourceId(t *testing.T) {
	mockCtx := mocks.NewMockContext(t.Context())
	svc := NewArmMsiService(
		mockCtx.SubscriptionCredentialProvider,
		mockCtx.ArmClientOptions,
	)

	_, err := svc.ApplyFederatedCredentials(
		t.Context(), testSubId, "bad-resource-id", nil,
	)
	require.Error(t, err)
	require.Contains(t, err.Error(), "parsing MSI resource id")
}

func TestApplyFederatedCredentials_SkipsExistingSubjects(t *testing.T) {
	mockCtx := mocks.NewMockContext(t.Context())

	// Mock: list existing creds returns one with "existing-subject"
	mockCtx.HttpClient.When(func(request *http.Request) bool {
		return request.Method == http.MethodGet &&
			strings.Contains(
				request.URL.Path,
				"federatedIdentityCredentials",
			)
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		body := armmsi.FederatedIdentityCredentialsListResult{
			Value: []*armmsi.FederatedIdentityCredential{
				{
					Name: new("existing-cred"),
					Properties: &armmsi.FederatedIdentityCredentialProperties{
						Subject: new("existing-subject"),
						Issuer:  new("https://issuer"),
					},
				},
			},
		}
		return mocks.CreateHttpResponseWithBody(
			request, http.StatusOK, body,
		)
	})

	// Mock: create call returns success; track invocations
	createCount := 0
	mockCtx.HttpClient.When(func(request *http.Request) bool {
		return request.Method == http.MethodPut &&
			strings.Contains(
				request.URL.Path,
				"federatedIdentityCredentials",
			)
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		createCount++
		body := armmsi.FederatedIdentityCredential{
			Name: new("new-cred"),
			Properties: &armmsi.FederatedIdentityCredentialProperties{
				Subject: new("new-subject"),
				Issuer:  new("https://issuer"),
			},
		}
		return mocks.CreateHttpResponseWithBody(
			request, http.StatusOK, body,
		)
	})

	svc := NewArmMsiService(
		mockCtx.SubscriptionCredentialProvider,
		mockCtx.ArmClientOptions,
	)

	requested := []armmsi.FederatedIdentityCredential{
		{
			Name: new("existing-cred"),
			Properties: &armmsi.FederatedIdentityCredentialProperties{
				Subject: new("existing-subject"),
				Issuer:  new("https://issuer"),
			},
		},
		{
			Name: new("new-cred"),
			Properties: &armmsi.FederatedIdentityCredentialProperties{
				Subject: new("new-subject"),
				Issuer:  new("https://issuer"),
			},
		},
	}

	result, err := svc.ApplyFederatedCredentials(
		t.Context(), testSubId, testMsiResId, requested,
	)
	require.NoError(t, err)

	// Only the new credential should have been created
	require.Equal(t, 1, createCount)
	require.Len(t, result, 1)
	require.Equal(t, "new-subject", *result[0].Properties.Subject)
}
