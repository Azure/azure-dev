package storage

import (
	"context"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/auth"
	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/stretchr/testify/require"
)

func Test_Items(t *testing.T) {
	t.Run("Success", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		client := createClientForTest(t, mockContext)

		items, err := client.Items(*mockContext.Context)
		require.NoError(t, err)
		require.Greater(t, len(items), 0)
	})
	t.Run("ContainerNotFound", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		client := createClientForTest(t, mockContext)

		items, err := client.Items(*mockContext.Context)
		require.Error(t, err)
		require.Nil(t, items)
	})
}

func createClientForTest(t *testing.T, mockContext *mocks.MockContext) BlobClient {
	accountConfig := AccountConfig{
		AccountName:   "account",
		ContainerName: "container",
	}

	configManager := config.NewManager()
	userConfigManager := config.NewUserConfigManager()

	authManager, err := auth.NewManager(configManager, userConfigManager, mockContext.HttpClient, mockContext.Console)
	require.NoError(t, err)

	return NewBlobClient(accountConfig, authManager, mockContext.HttpClient)
}
