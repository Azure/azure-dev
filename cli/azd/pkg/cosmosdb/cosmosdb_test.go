// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cosmosdb

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/cosmos/armcosmos/v2"
	"github.com/stretchr/testify/require"

	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockaccount"
)

func TestNewCosmosDbService(t *testing.T) {
	mockCtx := mocks.NewMockContext(t.Context())
	svc, err := NewCosmosDbService(
		mockCtx.SubscriptionCredentialProvider,
		mockCtx.ArmClientOptions,
	)
	require.NoError(t, err)
	require.NotNil(t, svc)
}

func TestConnectionString_CredentialError(t *testing.T) {
	expectedErr := errors.New("credential failure")
	credProvider := mockaccount.SubscriptionCredentialProviderFunc(
		func(_ context.Context, _ string) (azcore.TokenCredential, error) {
			return nil, expectedErr
		},
	)

	svc, err := NewCosmosDbService(credProvider, nil)
	require.NoError(t, err)

	_, err = svc.ConnectionString(
		t.Context(), "sub-id", "rg", "account",
	)
	require.ErrorIs(t, err, expectedErr)
}

func TestConnectionString_Success(t *testing.T) {
	mockCtx := mocks.NewMockContext(t.Context())

	expected := "AccountEndpoint=https://test.documents.azure.com:443/;" +
		"AccountKey=testkey123;"

	mockCtx.HttpClient.When(func(request *http.Request) bool {
		return request.Method == http.MethodPost &&
			strings.Contains(
				request.URL.Path, "listConnectionStrings",
			)
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		body := armcosmos.DatabaseAccountListConnectionStringsResult{
			ConnectionStrings: []*armcosmos.DatabaseAccountConnectionString{
				{ConnectionString: new(expected)},
			},
		}
		return mocks.CreateHttpResponseWithBody(
			request, http.StatusOK, body,
		)
	})

	svc, err := NewCosmosDbService(
		mockCtx.SubscriptionCredentialProvider,
		mockCtx.ArmClientOptions,
	)
	require.NoError(t, err)

	connStr, err := svc.ConnectionString(
		t.Context(), "sub-id", "rg", "myaccount",
	)
	require.NoError(t, err)
	require.Equal(t, expected, connStr)
}

func TestConnectionString_NilConnectionStrings(t *testing.T) {
	mockCtx := mocks.NewMockContext(t.Context())

	mockCtx.HttpClient.When(func(request *http.Request) bool {
		return request.Method == http.MethodPost &&
			strings.Contains(
				request.URL.Path, "listConnectionStrings",
			)
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		body := armcosmos.DatabaseAccountListConnectionStringsResult{
			ConnectionStrings: nil,
		}
		return mocks.CreateHttpResponseWithBody(
			request, http.StatusOK, body,
		)
	})

	svc, err := NewCosmosDbService(
		mockCtx.SubscriptionCredentialProvider,
		mockCtx.ArmClientOptions,
	)
	require.NoError(t, err)

	_, err = svc.ConnectionString(
		t.Context(), "sub-id", "rg", "myaccount",
	)
	require.Error(t, err)
	require.Contains(t, err.Error(), "connection strings are nil")
}

func TestConnectionString_EmptyConnectionStrings(t *testing.T) {
	mockCtx := mocks.NewMockContext(t.Context())

	mockCtx.HttpClient.When(func(request *http.Request) bool {
		return request.Method == http.MethodPost &&
			strings.Contains(
				request.URL.Path, "listConnectionStrings",
			)
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		body := armcosmos.DatabaseAccountListConnectionStringsResult{
			ConnectionStrings: []*armcosmos.DatabaseAccountConnectionString{},
		}
		return mocks.CreateHttpResponseWithBody(
			request, http.StatusOK, body,
		)
	})

	svc, err := NewCosmosDbService(
		mockCtx.SubscriptionCredentialProvider,
		mockCtx.ArmClientOptions,
	)
	require.NoError(t, err)

	_, err = svc.ConnectionString(
		t.Context(), "sub-id", "rg", "myaccount",
	)
	require.Error(t, err)
	require.Contains(t, err.Error(), "no connection strings found")
}
