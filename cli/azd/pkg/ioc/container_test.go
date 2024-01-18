package ioc

import (
	"errors"
	"testing"
	"time"

	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"github.com/stretchr/testify/require"
)

func Test_Container_Resolve(t *testing.T) {
	t.Run("Success", func(t *testing.T) {
		container := NewNestedContainer(nil)
		container.MustRegisterSingleton(func() string {
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
		container.MustRegisterSingleton(azdcontext.NewAzdContext)

		var instance *azdcontext.AzdContext
		// AzdContext resolver is registered above
		// Expect failure from no project
		err := container.Resolve(&instance)

		require.Error(t, err)
		require.False(t, errors.Is(err, ErrResolveInstance))
		require.True(t, errors.Is(err, azdcontext.ErrNoProject))
	})
}

func Test_Container_NewScope(t *testing.T) {
	rootContainer := NewNestedContainer(nil)
	rootContainer.MustRegisterSingleton(newSingletonService)
	rootContainer.MustRegisterScoped(newScopedService)

	var singletonInstance *singletonService
	err := rootContainer.Resolve(&singletonInstance)
	require.NoError(t, err)
	require.NotNil(t, singletonInstance)

	scope1, err := rootContainer.NewScope()
	require.NoError(t, err)
	var scopedInstance1 *scopedService

	err = scope1.Resolve(&scopedInstance1)
	require.NoError(t, err)
	require.NotNil(t, scopedInstance1)

	var singletonInstance2 *singletonService
	err = scope1.Resolve(&singletonInstance2)
	require.NoError(t, err)
	require.NotNil(t, singletonInstance2)

	// Singleton instance 1 & 2 are singleton still singletons and should be the same
	require.Same(t, singletonInstance, singletonInstance2)

	scope2, err := rootContainer.NewScope()
	require.NoError(t, err)
	var scopedInstance2 *scopedService

	err = scope2.Resolve(&scopedInstance2)
	require.NoError(t, err)
	require.NotNil(t, scopedInstance2)

	// Instance 1 & 2 are from different scopes and shot NOT be the same
	require.NotSame(t, scopedInstance1, scopedInstance2)

	// Instance 2 & 3 are from the same scope and should be the same
	var scopedInstance3 *scopedService
	err = scope2.Resolve(&scopedInstance3)
	require.NoError(t, err)
	require.NotNil(t, scopedInstance3)
	require.Same(t, scopedInstance2, scopedInstance3)
}

func Test_Container_Transient_Register_Resolve(t *testing.T) {
	container := NewNestedContainer(nil)
	container.MustRegisterTransient(newTransientService)

	var instance1 *transientService
	err := container.Resolve(&instance1)
	require.NoError(t, err)
	require.NotNil(t, instance1)

	var instance2 *transientService
	err = container.Resolve(&instance2)
	require.NoError(t, err)
	require.NotNil(t, instance2)

	// Instance 1 & 2 are transient and should NOT be the same
	require.NotSame(t, instance1, instance2)
}

func Test_Container_Singleton_Instance_Register_Resolve(t *testing.T) {
	t.Run("Same Scope", func(t *testing.T) {
		singletonInstance := newSingletonService()

		container := NewNestedContainer(nil)
		RegisterInstance(container, singletonInstance)

		var instance1 *singletonService
		err := container.Resolve(&instance1)
		require.NoError(t, err)
		require.NotNil(t, instance1)

		var instance2 *singletonService
		err = container.Resolve(&instance2)
		require.NoError(t, err)
		require.NotNil(t, instance2)

		// Instance 1 & 2 are singletons and should be the same
		require.Same(t, instance1, instance2)
	})

	t.Run("Nested Scope", func(t *testing.T) {
		rootContainer := NewNestedContainer(nil)

		rootInstance := newSingletonService()
		RegisterInstance(rootContainer, rootInstance)

		scope1, err := rootContainer.NewScope()
		require.NoError(t, err)
		scope1Instance := newSingletonService()
		RegisterInstance(scope1, scope1Instance)

		scope2, err := rootContainer.NewScope()
		require.NoError(t, err)
		scope2Instance := newSingletonService()
		RegisterInstance(scope2, scope2Instance)

		var rootInstanceResolved *singletonService
		err = rootContainer.Resolve(&rootInstanceResolved)
		require.NoError(t, err)
		require.NotNil(t, rootInstanceResolved)

		var scope1Instance1 *singletonService
		err = scope1.Resolve(&scope1Instance1)
		require.NoError(t, err)
		require.NotNil(t, scope1Instance1)

		var scope2Instance1 *singletonService
		err = scope2.Resolve(&scope2Instance1)
		require.NoError(t, err)
		require.NotNil(t, scope2Instance1)

		// Instance 1 & 2 are singletons but overriden in each child scope so they should be different
		require.NotSame(t, rootInstance, rootInstanceResolved)
		require.NotSame(t, scope1Instance, scope2Instance)
	})
}

type singletonService struct {
	timestamp time.Time
}

func newSingletonService() *singletonService {
	return &singletonService{
		timestamp: time.Now(),
	}
}

type scopedService struct {
	timestamp time.Time
}

func newScopedService() *scopedService {
	return &scopedService{
		timestamp: time.Now(),
	}
}

type transientService struct {
	timestamp time.Time
}

func newTransientService() *transientService {
	return &transientService{
		timestamp: time.Now(),
	}
}
