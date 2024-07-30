package platform

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/wbreza/container/v4"
)

func Test_Platform_Initialize(t *testing.T) {
	t.Run("ExplicitConfig", func(t *testing.T) {
		rootContainer := container.New()
		container.MustRegisterNamedSingleton(rootContainer, "default-platform", newDefaultProvider)
		container.MustRegisterNamedSingleton(rootContainer, "test-platform", newTestProvider)

		config := &Config{
			Type: PlatformKind("test"),
		}
		container.MustRegisterInstance(rootContainer, config)

		provider, err := Initialize(context.Background(), rootContainer, PlatformKind("default"))
		require.NoError(t, err)
		require.NotNil(t, provider)
		require.IsType(t, new(testProvider), provider)
		require.NoError(t, Error)
	})

	t.Run("ImplicitConfig", func(t *testing.T) {
		rootContainer := container.New()
		container.MustRegisterNamedSingleton(rootContainer, "default-platform", newDefaultProvider)
		container.MustRegisterNamedSingleton(rootContainer, "test-platform", newTestProvider)

		container.MustRegisterSingleton(rootContainer, func() (*Config, error) {
			return nil, ErrPlatformConfigNotFound
		})

		provider, err := Initialize(context.Background(), rootContainer, PlatformKind("default"))
		require.NoError(t, err)
		require.NotNil(t, provider)
		require.IsType(t, new(defaultProvider), provider)

		require.Error(t, Error)
		require.ErrorIs(t, Error, ErrPlatformConfigNotFound)
	})

	t.Run("Unsupported", func(t *testing.T) {
		rootContainer := container.New()
		container.MustRegisterNamedSingleton(rootContainer, "default-platform", newDefaultProvider)
		container.MustRegisterNamedSingleton(rootContainer, "test-platform", newTestProvider)

		container.MustRegisterSingleton(rootContainer, func() (*Config, error) {
			return nil, ErrPlatformNotSupported
		})

		provider, err := Initialize(context.Background(), rootContainer, PlatformKind("default"))
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

func (p *defaultProvider) ConfigureContainer(container *container.Container) error {
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

func (p *testProvider) ConfigureContainer(container *container.Container) error {
	return nil
}
