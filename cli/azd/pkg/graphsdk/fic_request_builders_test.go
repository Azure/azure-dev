package graphsdk_test

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/azure/azure-dev/cli/azd/pkg/convert"
	"github.com/azure/azure-dev/cli/azd/pkg/graphsdk"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockgraphsdk"
	"github.com/stretchr/testify/require"
)

var (
	application graphsdk.Application = graphsdk.Application{
		Id:          convert.RefOf("application-id"),
		DisplayName: "application name",
		Description: convert.RefOf("app description"),
	}

	federatedCredentials []graphsdk.FederatedIdentityCredential = []graphsdk.FederatedIdentityCredential{
		{
			Id:          convert.RefOf("cred-01"),
			Name:        "Credential 1",
			Issuer:      "ISSUER",
			Subject:     "SUBJECT",
			Description: convert.RefOf("DESCRIPTION"),
			Audiences:   []string{"AUDIENCE"},
		},
		{
			Id:          convert.RefOf("cred-02"),
			Name:        "Credential 2",
			Issuer:      "ISSUER",
			Subject:     "SUBJECT",
			Description: convert.RefOf("DESCRIPTION"),
		},
	}
)

func TestGetFederatedCredentialList(t *testing.T) {
	t.Run("Success", func(t *testing.T) {
		expected := append([]graphsdk.FederatedIdentityCredential{}, federatedCredentials...)

		mockContext := mocks.NewMockContext(context.Background())
		mockgraphsdk.RegisterFederatedCredentialsListMock(mockContext, *application.Id, http.StatusOK, expected)

		client, err := mockgraphsdk.CreateGraphClient(mockContext)
		require.NoError(t, err)

		res, err := client.
			ApplicationById(*application.Id).
			FederatedIdentityCredentials().
			Get(*mockContext.Context)

		require.NoError(t, err)
		require.NotNil(t, res)
		require.Equal(t, expected, res.Value)
	})

	t.Run("Error", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		mockgraphsdk.RegisterFederatedCredentialsListMock(mockContext, *application.Id, http.StatusUnauthorized, nil)

		client, err := mockgraphsdk.CreateGraphClient(mockContext)
		require.NoError(t, err)

		res, err := client.
			ApplicationById(*application.Id).
			FederatedIdentityCredentials().
			Get(*mockContext.Context)

		require.Error(t, err)
		require.Nil(t, res)
	})
}

func TestGetFederatedCredentialById(t *testing.T) {
	t.Run("Success", func(t *testing.T) {
		expected := federatedCredentials[0]

		mockContext := mocks.NewMockContext(context.Background())
		mockgraphsdk.RegisterFederatedCredentialGetItemMock(
			mockContext,
			*application.Id,
			*expected.Id,
			http.StatusOK,
			&expected,
		)

		client, err := mockgraphsdk.CreateGraphClient(mockContext)
		require.NoError(t, err)

		actual, err := client.
			ApplicationById(*application.Id).
			FederatedIdentityCredentialById(*expected.Id).
			Get(*mockContext.Context)

		require.NoError(t, err)
		require.NotNil(t, actual)
		require.Equal(t, *expected.Id, *actual.Id)
		require.Equal(t, expected.Name, actual.Name)
		require.Equal(t, expected.Issuer, actual.Issuer)
	})

	t.Run("Error", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		mockgraphsdk.RegisterFederatedCredentialGetItemMock(
			mockContext,
			*application.Id,
			"bad-id",
			http.StatusNotFound,
			nil,
		)

		client, err := mockgraphsdk.CreateGraphClient(mockContext)
		require.NoError(t, err)

		res, err := client.
			ApplicationById(*application.Id).
			FederatedIdentityCredentialById("bad-id").
			Get(*mockContext.Context)

		require.Error(t, err)
		require.Nil(t, res)
	})
}

func TestCreateFederatedCredential(t *testing.T) {
	t.Run("Success", func(t *testing.T) {
		expected := federatedCredentials[0]

		mockContext := mocks.NewMockContext(context.Background())
		mockgraphsdk.RegisterFederatedCredentialCreateItemMock(mockContext, *application.Id, http.StatusCreated, &expected)

		client, err := mockgraphsdk.CreateGraphClient(mockContext)
		require.NoError(t, err)

		actual, err := client.ApplicationById(*application.Id).
			FederatedIdentityCredentials().
			Post(*mockContext.Context, &expected)
		require.NoError(t, err)
		require.NotNil(t, actual)
		require.Equal(t, *expected.Id, *actual.Id)
		require.Equal(t, expected.Name, actual.Name)
		require.Equal(t, expected.Issuer, actual.Issuer)
	})

	t.Run("Error", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		mockgraphsdk.RegisterFederatedCredentialCreateItemMock(mockContext, *application.Id, http.StatusBadRequest, nil)

		client, err := mockgraphsdk.CreateGraphClient(mockContext)
		require.NoError(t, err)

		res, err := client.
			ApplicationById(*application.Id).
			FederatedIdentityCredentials().
			Post(*mockContext.Context, &graphsdk.FederatedIdentityCredential{})

		require.Error(t, err)
		require.Nil(t, res)
	})
}

func TestPatchFederatedCredential(t *testing.T) {
	expected := federatedCredentials[0]

	t.Run("Success", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		mockgraphsdk.RegisterFederatedCredentialPatchItemMock(
			mockContext,
			*application.Id,
			*expected.Id,
			http.StatusNoContent,
		)

		client, err := mockgraphsdk.CreateGraphClient(mockContext)
		require.NoError(t, err)

		err = client.
			ApplicationById(*application.Id).
			FederatedIdentityCredentialById(*expected.Id).
			Update(*mockContext.Context, &expected)

		require.NoError(t, err)
	})

	t.Run("Error", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		mockgraphsdk.RegisterFederatedCredentialPatchItemMock(
			mockContext,
			*application.Id,
			*expected.Id,
			http.StatusBadRequest,
		)

		client, err := mockgraphsdk.CreateGraphClient(mockContext)
		require.NoError(t, err)

		err = client.
			ApplicationById(*application.Id).
			FederatedIdentityCredentialById(*expected.Id).
			Update(*mockContext.Context, &graphsdk.FederatedIdentityCredential{})

		require.Error(t, err)
	})
}

func TestDeleteFederatedCredential(t *testing.T) {
	credentialId := "credential-to-delete"

	t.Run("Success", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		mockgraphsdk.RegisterFederatedCredentialDeleteItemMock(
			mockContext,
			*application.Id,
			credentialId,
			http.StatusNoContent,
		)

		client, err := mockgraphsdk.CreateGraphClient(mockContext)
		require.NoError(t, err)

		err = client.
			ApplicationById(*application.Id).
			FederatedIdentityCredentialById(credentialId).
			Delete(*mockContext.Context)

		require.NoError(t, err)
	})

	t.Run("Error", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		mockgraphsdk.RegisterFederatedCredentialDeleteItemMock(
			mockContext,
			*application.Id,
			credentialId,
			http.StatusNotFound,
		)

		client, err := mockgraphsdk.CreateGraphClient(mockContext)
		require.NoError(t, err)

		err = client.
			ApplicationById(*application.Id).
			FederatedIdentityCredentialById(credentialId).
			Delete(*mockContext.Context)
		require.Error(t, err)

		var httpErr *azcore.ResponseError
		require.True(t, errors.As(err, &httpErr))
		require.Equal(t, http.StatusNotFound, httpErr.StatusCode)
	})
}
