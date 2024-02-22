package cmd

import (
	"context"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/ioc"
	"github.com/azure/azure-dev/cli/azd/pkg/lazy"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
	"github.com/stretchr/testify/require"
)

func Test_Lazy_Project_Config_Resolution(t *testing.T) {
	ctx := context.Background()
	container := ioc.NewNestedContainer(nil)
	ioc.RegisterInstance(container, ctx)

	registerCommonDependencies(container)

	// Register the testing lazy component
	container.MustRegisterTransient(
		func(lazyProjectConfig *lazy.Lazy[*project.ProjectConfig]) *testLazyComponent[*project.ProjectConfig] {
			return &testLazyComponent[*project.ProjectConfig]{
				lazy: lazyProjectConfig,
			}
		},
	)

	// Register the testing concrete component
	container.MustRegisterTransient(
		func(projectConfig *project.ProjectConfig) *testConcreteComponent[*project.ProjectConfig] {
			return &testConcreteComponent[*project.ProjectConfig]{
				concrete: projectConfig,
			}
		},
	)

	// The lazy components depends on the lazy project config.
	// The lazy instance itself should never be nil
	var lazyComponent *testLazyComponent[*project.ProjectConfig]
	err := container.Resolve(&lazyComponent)
	require.NoError(t, err)
	require.NotNil(t, lazyComponent.lazy)

	// Get the lazy project config instance itself to use for comparison
	var lazyProjectConfig *lazy.Lazy[*project.ProjectConfig]
	err = container.Resolve(&lazyProjectConfig)
	require.NoError(t, err)
	require.NotNil(t, lazyProjectConfig)

	// At this point a project config is not available, so we should get an error
	projectConfig, err := lazyProjectConfig.GetValue()
	require.Nil(t, projectConfig)
	require.Error(t, err)

	// Set a project config on the lazy instance
	projectConfig = &project.ProjectConfig{
		Name: "test",
	}

	lazyProjectConfig.SetValue(projectConfig)

	// Now lets resolve a type that depends on a concrete project config
	// The project config should be be available not that the lazy has been set above
	var staticComponent *testConcreteComponent[*project.ProjectConfig]
	err = container.Resolve(&staticComponent)
	require.NoError(t, err)
	require.NotNil(t, staticComponent.concrete)

	// Now we validate that the instance returned by the lazy instance is the same as the one resolved directly
	lazyValue, err := lazyComponent.lazy.GetValue()
	require.NoError(t, err)
	directValue, err := lazyProjectConfig.GetValue()
	require.NoError(t, err)

	// Finally we validate that the return project config across all resolutions point to the same project config pointer
	require.Same(t, lazyProjectConfig, lazyComponent.lazy)
	require.Same(t, lazyValue, directValue)
	require.Same(t, directValue, staticComponent.concrete)
}

type testLazyComponent[T comparable] struct {
	lazy *lazy.Lazy[T]
}

type testConcreteComponent[T comparable] struct {
	concrete T
}
