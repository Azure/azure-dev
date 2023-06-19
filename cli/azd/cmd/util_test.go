package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/stretchr/testify/require"
)

func Test_promptEnvironmentName(t *testing.T) {
	t.Run("valid name", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		mockContext.Console.WhenPrompt(func(options input.ConsoleOptions) bool {
			return true
		}).SetError(errors.New("prompt should not be called for valid environment name"))

		environmentName := "hello"

		err := ensureValidEnvironmentName(*mockContext.Context, &environmentName, "", mockContext.Console)

		require.NoError(t, err)
	})

	t.Run("empty name gets prompted", func(t *testing.T) {
		environmentName := ""

		mockContext := mocks.NewMockContext(context.Background())
		mockContext.Console.WhenPrompt(func(options input.ConsoleOptions) bool {
			return true
		}).Respond("someEnv")

		err := ensureValidEnvironmentName(*mockContext.Context, &environmentName, "", mockContext.Console)

		require.NoError(t, err)
		require.Equal(t, "someEnv", environmentName)
	})
}

func Test_createAndInitEnvironment(t *testing.T) {
	t.Run("invalid name", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		tempDir := t.TempDir()
		azdContext := azdcontext.NewAzdContextWithDirectory(tempDir)
		invalidEnvName := "*!33"
		_, err := createEnvironment(
			*mockContext.Context,
			environmentSpec{
				environmentName: invalidEnvName,
			},
			azdContext,
			mockContext.Console,
		)
		require.ErrorContains(
			t,
			err,
			fmt.Sprintf("environment name '%s' is invalid (it should contain only alphanumeric characters and hyphens)\n",
				invalidEnvName))
	})

	t.Run("env already exists", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		tempDir := t.TempDir()
		validName := "azdEnv"
		err := os.MkdirAll(filepath.Join(tempDir, ".azure", validName), 0755)
		require.NoError(t, err)
		azdContext := azdcontext.NewAzdContextWithDirectory(tempDir)

		_, err = createEnvironment(
			*mockContext.Context,
			environmentSpec{
				environmentName: validName,
			},
			azdContext,
			mockContext.Console,
		)
		require.ErrorContains(
			t,
			err,
			fmt.Sprintf("environment '%s' already exists",
				validName))
	})
}
