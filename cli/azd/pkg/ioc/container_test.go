package ioc

import (
	"errors"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"github.com/stretchr/testify/require"
)

func Test_Resolve(t *testing.T) {
	t.Run("Success", func(t *testing.T) {
		container := NewNestedContainer(nil)
		container.RegisterSingleton(func() string {
			return "Test"
		})

		var instance string
		err := container.Resolve(&instance)

		require.NoError(t, err)
		require.Equal(t, "Test", instance)
	})

	t.Run("FailWithContainerError", func(t *testing.T) {
		container := NewNestedContainer(nil)

		var instance *azdcontext.AzdContext
		// Since a resolver wasn't registered for AzdContext
		// Expect a resolution container failure
		err := container.Resolve(&instance)

		require.Error(t, err)
		require.True(t, errors.Is(err, ErrResolveInstance))
		require.False(t, errors.Is(err, azdcontext.ErrNoProject))
	})

	t.Run("FailWithOtherError", func(t *testing.T) {
		container := NewNestedContainer(nil)
		container.RegisterSingleton(azdcontext.NewAzdContext)

		var instance *azdcontext.AzdContext
		// AzdContext resolver is registered above
		// Expect failure from no project
		err := container.Resolve(&instance)

		require.Error(t, err)
		require.False(t, errors.Is(err, ErrResolveInstance))
		require.True(t, errors.Is(err, azdcontext.ErrNoProject))
	})
}
