package middleware

import (
	"context"
	"errors"
	"testing"

	"github.com/azure/azure-dev/cli/azd/cmd/actions"
	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"github.com/azure/azure-dev/cli/azd/pkg/lazy"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockenv"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func Test_Environment_Already_Exists(t *testing.T) {
	expectedEnv := environment.NewWithValues("test", map[string]string{
		environment.SubscriptionIdEnvVarName: "SUBSCRIPTION_ID",
	})

	mockContext := mocks.NewMockContext(context.Background())
	azdContext := azdcontext.NewAzdContextWithDirectory(t.TempDir())

	middleware, lazyEnv := createMiddlewareForTest(azdContext, expectedEnv, internal.EnvFlag{}, &mockenv.MockEnvManager{})
	result, err := middleware.Run(*mockContext.Context, nextFn)
	require.NoError(t, err)
	require.NotNil(t, result)

	actualEnv, err := lazyEnv.GetValue()
	require.NoError(t, err)
	require.NotNil(t, actualEnv)
	require.Equal(t, expectedEnv.Name(), actualEnv.Name())
}

func Test_Environment_No_Azd_Context(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())

	middleware, lazyEnv := createMiddlewareForTest(nil, nil, internal.EnvFlag{}, &mockenv.MockEnvManager{})
	result, err := middleware.Run(*mockContext.Context, nextFn)
	require.NoError(t, err)
	require.NotNil(t, result)

	actualEnv, err := lazyEnv.GetValue()
	require.Error(t, err)
	require.Nil(t, actualEnv)
}

func Test_Environment_With_Flag(t *testing.T) {
	expectedEnv := environment.NewWithValues("flag-env", map[string]string{})

	mockContext := mocks.NewMockContext(context.Background())
	azdContext := azdcontext.NewAzdContextWithDirectory(t.TempDir())
	envFlag := internal.EnvFlag{EnvironmentName: expectedEnv.Name()}

	envManager := &mockenv.MockEnvManager{}
	envManager.On("LoadOrInitInteractive", mock.Anything, mock.Anything).Return(expectedEnv, nil)

	middleware, lazyEnv := createMiddlewareForTest(azdContext, nil, envFlag, envManager)
	result, err := middleware.Run(*mockContext.Context, nextFn)
	require.NoError(t, err)
	require.NotNil(t, result)

	actualEnv, err := lazyEnv.GetValue()
	require.NoError(t, err)
	require.NotNil(t, actualEnv)
	require.Equal(t, expectedEnv.Name(), actualEnv.Name())
}

func Test_Environment_From_Prompt(t *testing.T) {
	expectedEnv := environment.NewWithValues("prompt-env", map[string]string{})

	mockContext := mocks.NewMockContext(context.Background())
	azdContext := azdcontext.NewAzdContextWithDirectory(t.TempDir())

	envManager := &mockenv.MockEnvManager{}
	envManager.On("LoadOrInitInteractive", mock.Anything, mock.Anything).Return(expectedEnv, nil)

	middleware, lazyEnv := createMiddlewareForTest(azdContext, nil, internal.EnvFlag{}, envManager)
	result, err := middleware.Run(*mockContext.Context, nextFn)
	require.NoError(t, err)
	require.NotNil(t, result)

	actualEnv, err := lazyEnv.GetValue()
	require.NoError(t, err)
	require.NotNil(t, actualEnv)
	require.Equal(t, expectedEnv.Name(), actualEnv.Name())
}

func Test_Environment_From_Default(t *testing.T) {
	expectedEnv := environment.NewWithValues("default-env", map[string]string{})

	mockContext := mocks.NewMockContext(context.Background())
	azdContext := azdcontext.NewAzdContextWithDirectory(t.TempDir())
	err := azdContext.SetProjectState(azdcontext.ProjectState{
		DefaultEnvironment: expectedEnv.Name(),
	})
	require.NoError(t, err)

	envManager := &mockenv.MockEnvManager{}
	envManager.On("LoadOrInitInteractive", mock.Anything, mock.Anything).Return(expectedEnv, nil)

	middleware, lazyEnv := createMiddlewareForTest(azdContext, nil, internal.EnvFlag{}, envManager)
	result, err := middleware.Run(*mockContext.Context, nextFn)
	require.NoError(t, err)
	require.NotNil(t, result)

	actualEnv, err := lazyEnv.GetValue()
	require.NoError(t, err)
	require.NotNil(t, actualEnv)
	require.Equal(t, expectedEnv.Name(), actualEnv.Name())
}

func createMiddlewareForTest(
	azdContext *azdcontext.AzdContext,
	env *environment.Environment,
	envFlag internal.EnvFlag,
	mockEnvManager *mockenv.MockEnvManager,
) (Middleware, *lazy.Lazy[*environment.Environment]) {
	// Setup environment mocks for save & reload
	mockEnvManager.On("Save", mock.Anything, mock.Anything).Return(nil)
	mockEnvManager.On("Reload", mock.Anything, mock.Anything).Return(nil)

	lazyAzdContext := lazy.NewLazy(func() (*azdcontext.AzdContext, error) {
		if azdContext == nil {
			return nil, azdcontext.ErrNoProject
		}

		return azdContext, nil
	})

	lazyEnvManager := lazy.NewLazy(func() (environment.Manager, error) {
		return mockEnvManager, nil
	})

	lazyEnv := lazy.NewLazy(func() (*environment.Environment, error) {
		if env == nil {
			return nil, errors.New("environemnt not found")
		}

		return env, nil
	})

	return NewEnvironmentMiddleware(lazyAzdContext, lazyEnvManager, lazyEnv, envFlag), lazyEnv
}

func nextFn(ctx context.Context) (*actions.ActionResult, error) {
	return &actions.ActionResult{}, nil
}
