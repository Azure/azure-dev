package platform

import (
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/ioc"
	"github.com/stretchr/testify/require"
)

func Test_Platform_Initialize(t *testing.T) {
	t.Run("ExplicitConfig", func(t *testing.T) {
		container := ioc.NewNestedContainer(nil)
		container.MustRegisterNamedSingleton("default-platform", newDefaultProvider)
		container.MustRegisterNamedSingleton("test-platform", newTestProvider)

		config := &Config{
			Type: PlatformKind("test"),
		}
		ioc.RegisterInstance(container, config)

		provider, err := Initialize(container, PlatformKind("default"))
		require.NoError(t, err)
		require.NotNil(t, provider)
		require.IsType(t, new(testProvider), provider)
		require.NoError(t, Error)
	})

	t.Run("ImplicitConfig", func(t *testing.T) {
		container := ioc.NewNestedContainer(nil)
		container.MustRegisterNamedSingleton("default-platform", newDefaultProvider)
		container.MustRegisterNamedSingleton("test-platform", newTestProvider)

		container.MustRegisterSingleton(func() (*Config, error) {
			return nil, ErrPlatformConfigNotFound
		})

		provider, err := Initialize(container, PlatformKind("default"))
		require.NoError(t, err)
		require.NotNil(t, provider)
		require.IsType(t, new(defaultProvider), provider)

		require.Error(t, Error)
		require.ErrorIs(t, Error, ErrPlatformConfigNotFound)
	})

	t.Run("Unsupported", func(t *testing.T) {
		container := ioc.NewNestedContainer(nil)
		container.MustRegisterNamedSingleton("default-platform", newDefaultProvider)
		container.MustRegisterNamedSingleton("test-platform", newTestProvider)

		container.MustRegisterSingleton(func() (*Config, error) {
			return nil, ErrPlatformNotSupported
		})

		provider, err := Initialize(container, PlatformKind("default"))
		require.NoError(t, err)
		require.NotNil(t, provider)
		require.IsType(t, new(defaultProvider), provider)

		require.Error(t, Error)
		require.ErrorIs(t, Error, ErrPlatformNotSupported)
	})
}

type defaultProvider struct {
}

func newDefaultProvider() Provider {
	return &defaultProvider{}
}

func (p *defaultProvider) Name() string {
	return "default"
}

func (p *defaultProvider) IsEnabled() bool {
	return true
}

func (p *defaultProvider) ConfigureContainer(container *ioc.NestedContainer) error {
	return nil
}

type testProvider struct {
}

func newTestProvider() Provider {
	return &testProvider{}
}

func (p *testProvider) Name() string {
	return "test"
}

func (p *testProvider) IsEnabled() bool {
	return true
}

func (p *testProvider) ConfigureContainer(container *ioc.NestedContainer) error {
	return nil
}
