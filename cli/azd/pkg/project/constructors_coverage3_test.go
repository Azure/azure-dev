package project

import (
	"context"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/alpha"
	"github.com/azure/azure-dev/cli/azd/pkg/azapi"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/ioc"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func Test_NewProjectManager_Coverage3(t *testing.T) {
	pm := NewProjectManager(nil, nil, nil)
	require.NotNil(t, pm)
}

func Test_NewDotNetImporter_Coverage3(t *testing.T) {
	imp := NewDotNetImporter(nil, nil, nil, nil, nil)
	require.NotNil(t, imp)
	// Verify cache maps initialized
	assert.NotNil(t, imp.cache)
	assert.NotNil(t, imp.hostCheck)
}

func Test_NewServiceManager_Coverage3(t *testing.T) {
	env := environment.NewWithValues("test", map[string]string{})
	container := ioc.NewNestedContainer(nil)
	cache := ServiceOperationCache{}
	afm := alpha.NewFeaturesManagerWithConfig(nil)

	sm := NewServiceManager(env, nil, container, cache, afm)
	require.NotNil(t, sm)
}

func Test_NewExternalFrameworkService_Coverage3(t *testing.T) {
	svc := NewExternalFrameworkService("test-lang", ServiceLanguageCustom, nil, nil, nil)
	require.NotNil(t, svc)
}

func Test_NewExternalServiceTarget_Coverage3(t *testing.T) {
	target := NewExternalServiceTarget("test-target", ContainerAppTarget, nil, nil, nil, nil, nil)
	require.NotNil(t, target)
}

func Test_externalTool_Methods_Coverage3(t *testing.T) {
	tool := &externalTool{name: "my-tool", installUrl: "https://example.com/install"}

	t.Run("CheckInstalled_ReturnsNil", func(t *testing.T) {
		err := tool.CheckInstalled(context.Background())
		assert.NoError(t, err)
	})

	t.Run("Name", func(t *testing.T) {
		assert.Equal(t, "my-tool", tool.Name())
	})

	t.Run("InstallUrl", func(t *testing.T) {
		assert.Equal(t, "https://example.com/install", tool.InstallUrl())
	})
}

func Test_validateTargetResource_Coverage3(t *testing.T) {
	target := &dotnetContainerAppTarget{}

	t.Run("EmptyResourceGroup_Error", func(t *testing.T) {
		tr := environment.NewTargetResource("sub-id", "", "res-name", "")
		err := target.validateTargetResource(tr)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "missing resource group name")
	})

	t.Run("WrongResourceType_Error", func(t *testing.T) {
		tr := environment.NewTargetResource("sub-id", "my-rg", "res-name", "Microsoft.Web/sites")
		err := target.validateTargetResource(tr)
		require.Error(t, err)
	})

	t.Run("CorrectResourceType_OK", func(t *testing.T) {
		tr := environment.NewTargetResource(
			"sub-id", "my-rg", "res-name",
			string(azapi.AzureResourceTypeContainerAppEnvironment),
		)
		err := target.validateTargetResource(tr)
		require.NoError(t, err)
	})

	t.Run("EmptyResourceType_OK", func(t *testing.T) {
		tr := environment.NewTargetResource("sub-id", "my-rg", "res-name", "")
		err := target.validateTargetResource(tr)
		require.NoError(t, err)
	})
}

func Test_appServiceTarget_Publish_Coverage3(t *testing.T) {
	target := &appServiceTarget{}
	result, err := target.Publish(context.Background(), nil, nil, nil, nil, nil)
	require.NoError(t, err)
	require.NotNil(t, result)
}
