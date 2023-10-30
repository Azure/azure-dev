package platform

import (
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/ioc"
	"github.com/stretchr/testify/require"
)

func Test_Platform_Initialize(t *testing.T) {
	t.Run("ExplicitdConfig", func(t *testing.T) {
		container := ioc.NewNestedContainer(nil)
		_ = container.RegisterNamedSingleton("default-platform", newDefaultProvider)
		_ = container.RegisterNamedSingleton("test-platform", newTestProvider)

		config := &Config{
			Type: PlatformKind("test"),
		}
		ioc.RegisterInstance(container, config)

		provider, err := Initialize(container, PlatformKind("default"))
		require.NoError(t, err)
		require.NotNil(t, provider)
		require.IsType(t, new(testProvider), provider)
	})

	t.Run("ImplicitConfig", func(t *testing.T) {
		container := ioc.NewNestedContainer(nil)
		_ = container.RegisterNamedSingleton("default-platform", newDefaultProvider)
		_ = container.RegisterNamedSingleton("test-platform", newTestProvider)

		provider, err := Initialize(container, PlatformKind("default"))
		require.NoError(t, err)
		require.NotNil(t, provider)
		require.IsType(t, new(defaultProvider), provider)
	})

	t.Run("Unsupported", func(t *testing.T) {
		container := ioc.NewNestedContainer(nil)
		_ = container.RegisterNamedSingleton("default-platform", newDefaultProvider)
		_ = container.RegisterNamedSingleton("test-platform", newTestProvider)

		container.RegisterSingleton(func() (*Config, error) {
			return nil, ErrPlatformNotSupported
		})

		provider, err := Initialize(container, PlatformKind("default"))
		require.Error(t, err)
		require.ErrorIs(t, err, ErrPlatformNotSupported)
		require.Nil(t, provider)
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
