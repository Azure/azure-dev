package environment

import (
	"context"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/stretchr/testify/require"
)

func Test_LocalFileDataStore_List(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())
	azdContext := azdcontext.NewAzdContextWithDirectory(t.TempDir())
	fileConfigManager := config.NewFileConfigManager(config.NewManager())
	dataStore := NewLocalFileDataStore(azdContext, fileConfigManager)

	t.Run("List", func(t *testing.T) {
		env1 := New("env1", azdContext.EnvironmentRoot("env1"))
		err := dataStore.Save(*mockContext.Context, env1)
		require.NoError(t, err)

		env2 := New("env2", azdContext.EnvironmentRoot("env2"))
		err = dataStore.Save(*mockContext.Context, env2)
		require.NoError(t, err)

		envList, err := dataStore.List(*mockContext.Context)
		require.NoError(t, err)
		require.NotNil(t, envList)
		require.Equal(t, 2, len(envList))
	})
	t.Run("Empty", func(t *testing.T) {
		envList, err := dataStore.List(*mockContext.Context)
		require.NoError(t, err)
		require.NotNil(t, envList)
	})
}
