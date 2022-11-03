package graphsdk_test

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/azure/azure-dev/cli/azd/pkg/azsdk"
	"github.com/azure/azure-dev/cli/azd/pkg/convert"
	"github.com/azure/azure-dev/cli/azd/pkg/graphsdk"
	"github.com/stretchr/testify/require"
)

// Testing for temp integration testing during development
func Test_GraphClientRequest(t *testing.T) {
	ctx := context.Background()
	credential, err := azidentity.NewAzureCLICredential(nil)
	require.NoError(t, err)

	clientOptions := azsdk.NewClientOptionsBuilder().BuildCoreClientOptions()
	client, err := graphsdk.NewGraphClient(credential, clientOptions)
	require.NoError(t, err)
	require.NotNil(t, client)

	appsResponse, err := client.
		Applications().
		Filter("displayName eq 'wabrez-spn-go-test'").
		Get(ctx)

	require.NoError(t, err)
	require.NotNil(t, appsResponse)

	application := appsResponse.Value[0]

	ficsResponse, err := client.
		ApplicationById(*application.Id).
		FederatedIdentityCredentials().
		Get(ctx)

	require.NoError(t, err)
	require.NotNil(t, ficsResponse)

	var fic graphsdk.FederatedIdentityCredential

	if len(ficsResponse.Value) == 0 {
		createFic := graphsdk.FederatedIdentityCredential{
			Name:        "mainfic",
			Issuer:      "https://token.actions.githubusercontent.com",
			Subject:     "repo:${REPO}:ref:refs/heads/main",
			Description: convert.RefOf("main"),
			Audiences: []string{
				"api://AzureADTokenExchange",
			},
		}

		newFic, err := client.
			ApplicationById(*application.Id).
			FederatedIdentityCredentials().
			Post(ctx, &createFic)

		require.NoError(t, err)
		require.NotNil(t, newFic)

		fic = *newFic
	} else {
		fic = ficsResponse.Value[0]
	}

	existingFic, err := client.
		ApplicationById(*application.Id).
		FederatedIdentityCredentialById(*fic.Id).
		Get(ctx)

	require.NoError(t, err)
	require.NotNil(t, existingFic)

	existingFic.Description = convert.RefOf("updated")
	err = client.
		ApplicationById(*application.Id).
		FederatedIdentityCredentialById(*fic.Id).
		Update(ctx, existingFic)

	require.NoError(t, err)

	err = client.
		ApplicationById(*application.Id).
		FederatedIdentityCredentialById(*fic.Id).
		Delete(ctx)

	require.NoError(t, err)

	getFic, err := client.
		ApplicationById(*application.Id).
		FederatedIdentityCredentialById(*fic.Id).
		Get(ctx)

	require.Error(t, err)
	require.Nil(t, getFic)

	var httpErr *azcore.ResponseError
	require.True(t, errors.As(err, &httpErr))
	require.Equal(t, http.StatusNotFound, httpErr.StatusCode)
}
