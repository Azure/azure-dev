package azd

import (
	"context"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning"
	"github.com/stretchr/testify/require"
	"github.com/wbreza/container/v4"
)

func Test_DefaultPlatform_IsEnabled(t *testing.T) {
	t.Run("Enabled", func(t *testing.T) {
		defaultPlatform := NewDefaultPlatform()
		require.True(t, defaultPlatform.IsEnabled())
	})
}

func Test_DefaultPlatform_ConfigureContainer(t *testing.T) {
	t.Run("Success", func(t *testing.T) {
		defaultPlatform := NewDefaultPlatform()
		rootContainer := container.New()
		err := defaultPlatform.ConfigureContainer(rootContainer)
		require.NoError(t, err)

		var provisionResolver provisioning.DefaultProviderResolver
		err = rootContainer.Resolve(context.Background(), &provisionResolver)
		require.NoError(t, err)
		require.NotNil(t, provisionResolver)

		expected := provisioning.Bicep
		actual, err := provisionResolver()
		require.NoError(t, err)
		require.Equal(t, expected, actual)
	})
}
