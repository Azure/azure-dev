package azure

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func Test_GetResourceGroupName(t *testing.T) {
	t.Run("WithMatch", func(t *testing.T) {
		test := "/subscriptions/faa080af-c1d8-40ad-9cce-e1a450ca5b57/" +
			"resourceGroups/RESOURCE_GROUP_NAME/providers/" +
			"Microsoft.ContainerRegistry/registries/REGISTRY_NAME"
		resourceGroup := GetResourceGroupName(test)

		require.Equal(t, "RESOURCE_GROUP_NAME", *resourceGroup)
	})

	t.Run("WithMatchLower", func(t *testing.T) {
		test := "/subscriptions/faa080af-c1d8-40ad-9cce-e1a450ca5b57/" +
			"resourcegroups/RESOURCE_GROUP_NAME/providers/Microsoft.ContainerRegistry/" +
			"registries/REGISTRY_NAME"
		resourceGroup := GetResourceGroupName(test)

		require.Equal(t, "RESOURCE_GROUP_NAME", *resourceGroup)
	})

	t.Run("NoMatch", func(t *testing.T) {
		test := "i don't have what your looking for"
		resourceGroup := GetResourceGroupName(test)

		require.Nil(t, resourceGroup)
	})
}
