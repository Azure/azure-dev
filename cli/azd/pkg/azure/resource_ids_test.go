package azure

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func Test_GetResourceGroupName(t *testing.T) {
	t.Run("WithMatch", func(t *testing.T) {
		test := "/subscriptions/70a036f6-8e4d-4615-bad6-149c02e7720d/" +
			"resourceGroups/RESOURCE_GROUP_NAME/providers/" +
			"Microsoft.ContainerRegistry/registries/REGISTRY_NAME"
		resourceGroup := GetResourceGroupName(test)

		require.Equal(t, "RESOURCE_GROUP_NAME", *resourceGroup)
	})

	t.Run("WithMatchLower", func(t *testing.T) {
		test := "/subscriptions/70a036f6-8e4d-4615-bad6-149c02e7720d/" +
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
