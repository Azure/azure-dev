package devcenter

import (
	"context"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning"
	"github.com/azure/azure-dev/cli/azd/pkg/platform"
	"github.com/stretchr/testify/require"
	"github.com/wbreza/container/v4"
)

func Test_Platform_IsEnabled(t *testing.T) {
	t.Run("Enabled", func(t *testing.T) {
		config := &platform.Config{
			Type: PlatformKindDevCenter,
		}

		devCenterPlatform := NewPlatform(config)
		require.True(t, devCenterPlatform.IsEnabled())
	})
	t.Run("Disabled", func(t *testing.T) {
		config := &platform.Config{
			Type: platform.PlatformKind("default"),
		}

		devCenterPlatform := NewPlatform(config)
		require.False(t, devCenterPlatform.IsEnabled())
	})
}

func Test_Platform_ConfigureContainer(t *testing.T) {
	t.Run("Success", func(t *testing.T) {
		config := &platform.Config{
			Type: PlatformKindDevCenter,
		}

		devCenterPlatform := NewPlatform(config)
		rootContainer := container.New()
		err := devCenterPlatform.ConfigureContainer(rootContainer)
		require.NoError(t, err)

		var provisionResolver provisioning.DefaultProviderResolver
		err = rootContainer.Resolve(context.Background(), &provisionResolver)
		require.NoError(t, err)
		require.NotNil(t, provisionResolver)

		expected := ProvisionKindDevCenter
		actual, err := provisionResolver()
		require.NoError(t, err)
		require.Equal(t, expected, actual)
	})
}
