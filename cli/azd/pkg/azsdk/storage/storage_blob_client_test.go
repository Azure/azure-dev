package storage

import (
	"bytes"
	"context"
	"io"
	"os"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/auth"
	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/azure/azure-dev/cli/azd/test/recording"
	"github.com/stretchr/testify/require"
)

func Test_StorageBlobClient_Crud(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())
	session := recording.Start(t)
	blobClient := createBlobClient(t, mockContext, session.ProxyClient)

	blobPath := "test-env/.env"
	envValues := "KEY=VALUE\n"

	// Upload
	reader := bytes.NewBuffer([]byte(envValues))
	err := blobClient.Upload(*mockContext.Context, blobPath, reader)
	require.NoError(t, err)

	// Download
	downloadReader, err := blobClient.Download(*mockContext.Context, blobPath)
	require.NoError(t, err)
	require.NotNil(t, downloadReader)

	downloadBytes, err := io.ReadAll(downloadReader)
	require.NoError(t, err)
	require.Equal(t, envValues, string(downloadBytes))

	// List
	blobs, err := blobClient.Items(*mockContext.Context)
	require.NoError(t, err)
	require.NotEmpty(t, blobs)

	// Delete
	err = blobClient.Delete(*mockContext.Context, blobPath)
	require.NoError(t, err)
}

func createBlobClient(t *testing.T, mockContext *mocks.MockContext, httpClient auth.HttpClient) BlobClient {
	storageConfig := &AccountConfig{
		AccountName:   os.Getenv("AZD_TEST_REMOTE_STATE_ACCOUNT"),
		ContainerName: os.Getenv("AZD_TEST_REMOTE_STATE_CONTAINER"),
	}

	fileConfigManager := config.NewFileConfigManager(config.NewManager())

	authManager, err := auth.NewManager(
		fileConfigManager,
		config.NewUserConfigManager(fileConfigManager),
		httpClient, mockContext.Console,
	)
	require.NoError(t, err)

	credentials, err := authManager.CredentialForCurrentUser(*mockContext.Context, nil)
	require.NoError(t, err)

	sdkClient, err := NewBlobSdkClient(*mockContext.Context, credentials, storageConfig, httpClient, "azd")
	require.NoError(t, err)
	require.NotNil(t, sdkClient)

	return NewBlobClient(storageConfig, sdkClient)
}
