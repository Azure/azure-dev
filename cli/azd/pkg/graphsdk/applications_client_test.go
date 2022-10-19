package graphsdk

import (
	"context"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/stretchr/testify/require"
)

func TestListApplications(t *testing.T) {
	// TODO: Not really a test, just debugging implementation. Will fix.
	ctx := context.Background()
	creds, err := azidentity.NewAzureCLICredential(nil)
	require.NoError(t, err)

	client, err := NewGraphClient(creds, nil)
	require.NoError(t, err)

	response, err := client.
		Applications().
		Top(10).
		Get(ctx)

	require.NoError(t, err)
	require.Greater(t, len(response.Value), 0)

	item, err := client.ApplicationById("00000402-96ac-4626-ab60-7ee1a9361c68").Get(ctx)

	require.NotNil(t, item)
	require.NoError(t, err)
}
