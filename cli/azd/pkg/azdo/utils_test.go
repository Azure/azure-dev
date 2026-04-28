// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azdo

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockenv"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockinput"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestEnsureConfigExists(t *testing.T) {
	t.Run("value present in dotenv", func(t *testing.T) {
		env := environment.NewWithValues("test", map[string]string{
			"SOME_KEY": "some-value",
		})
		value, err := ensureConfigExists(env, "SOME_KEY", "some label")
		require.NoError(t, err)
		assert.Equal(t, "some-value", value)
	})

	t.Run("missing value returns error", func(t *testing.T) {
		env := environment.NewWithValues("test", map[string]string{})
		// Ensure no stray OS env collides with the probe key.
		const key = "AZDO_TEST_MISSING_KEY_XYZ"
		os.Unsetenv(key)
		value, err := ensureConfigExists(env, key, "my label")
		require.Error(t, err)
		assert.Empty(t, value)
		assert.Contains(t, err.Error(), "my label not found in environment variable "+key)
	})

	t.Run("empty value returns error", func(t *testing.T) {
		env := environment.NewWithValues("test", map[string]string{
			"EMPTY_KEY": "",
		})
		value, err := ensureConfigExists(env, "EMPTY_KEY", "label2")
		require.Error(t, err)
		assert.Empty(t, value)
		assert.Contains(t, err.Error(), "label2")
	})
}

func TestEnsurePatExists(t *testing.T) {
	t.Run("pat already in env returns it without prompting", func(t *testing.T) {
		env := environment.NewWithValues("test", map[string]string{
			AzDoPatName: "existing-pat",
		})
		mockConsole := mockinput.NewMockConsole()

		value, promptRequired, err := EnsurePatExists(t.Context(), env, mockConsole)
		require.NoError(t, err)
		assert.Equal(t, "existing-pat", value)
		assert.False(t, promptRequired)
	})

	t.Run("missing pat prompts user and sets env", func(t *testing.T) {
		// Ensure the OS env doesn't have the key.
		original, hadOriginal := os.LookupEnv(AzDoPatName)
		os.Unsetenv(AzDoPatName)
		t.Cleanup(func() {
			if hadOriginal {
				os.Setenv(AzDoPatName, original)
			} else {
				os.Unsetenv(AzDoPatName)
			}
		})

		env := environment.NewWithValues("test", map[string]string{})
		mockConsole := mockinput.NewMockConsole()
		mockConsole.WhenPrompt(func(options input.ConsoleOptions) bool {
			return strings.Contains(options.Message, "Personal Access Token")
		}).Respond("prompted-pat")

		value, promptRequired, err := EnsurePatExists(t.Context(), env, mockConsole)
		require.NoError(t, err)
		assert.Equal(t, "prompted-pat", value)
		assert.True(t, promptRequired)
		// The function sets the env var so subsequent lookups would succeed.
		got, ok := os.LookupEnv(AzDoPatName)
		require.True(t, ok)
		assert.Equal(t, "prompted-pat", got)
	})

	t.Run("prompt error is wrapped", func(t *testing.T) {
		original, hadOriginal := os.LookupEnv(AzDoPatName)
		os.Unsetenv(AzDoPatName)
		t.Cleanup(func() {
			if hadOriginal {
				os.Setenv(AzDoPatName, original)
			} else {
				os.Unsetenv(AzDoPatName)
			}
		})

		env := environment.NewWithValues("test", map[string]string{})
		mockConsole := mockinput.NewMockConsole()
		mockConsole.WhenPrompt(func(options input.ConsoleOptions) bool {
			return strings.Contains(options.Message, "Personal Access Token")
		}).RespondFn(func(_ input.ConsoleOptions) (any, error) {
			return "", fmt.Errorf("user aborted")
		})

		value, promptRequired, err := EnsurePatExists(t.Context(), env, mockConsole)
		require.Error(t, err)
		assert.Empty(t, value)
		assert.False(t, promptRequired)
		assert.Contains(t, err.Error(), "asking for pat")
	})
}

func TestEnsureOrgNameExists(t *testing.T) {
	t.Run("org already present returns it", func(t *testing.T) {
		env := environment.NewWithValues("test", map[string]string{
			AzDoEnvironmentOrgName: "my-org",
		})
		mockConsole := mockinput.NewMockConsole()
		mgr := &mockenv.MockEnvManager{}

		value, promptRequired, err := EnsureOrgNameExists(t.Context(), mgr, env, mockConsole)
		require.NoError(t, err)
		assert.Equal(t, "my-org", value)
		assert.False(t, promptRequired)
		// Save should not have been called.
		mgr.AssertNotCalled(t, "Save", mock.Anything, mock.Anything)
	})

	t.Run("missing org prompts and saves", func(t *testing.T) {
		// Ensure the OS env doesn't contain a value for the key.
		original, hadOriginal := os.LookupEnv(AzDoEnvironmentOrgName)
		os.Unsetenv(AzDoEnvironmentOrgName)
		t.Cleanup(func() {
			if hadOriginal {
				os.Setenv(AzDoEnvironmentOrgName, original)
			} else {
				os.Unsetenv(AzDoEnvironmentOrgName)
			}
		})

		env := environment.NewWithValues("test", map[string]string{})
		mockConsole := mockinput.NewMockConsole()
		mockConsole.WhenPrompt(func(options input.ConsoleOptions) bool {
			return strings.Contains(options.Message, "Azure DevOps Organization")
		}).Respond("new-org")

		mgr := &mockenv.MockEnvManager{}
		mgr.On("Save", mock.Anything, env).Return(nil)

		value, promptRequired, err := EnsureOrgNameExists(t.Context(), mgr, env, mockConsole)
		require.NoError(t, err)
		assert.Equal(t, "new-org", value)
		assert.True(t, promptRequired)
		// DotenvSet should have been persisted via envManager.Save.
		mgr.AssertCalled(t, "Save", mock.Anything, env)
		// The .env map should have been updated.
		got, ok := env.Dotenv()[AzDoEnvironmentOrgName]
		require.True(t, ok)
		assert.Equal(t, "new-org", got)
	})

	t.Run("prompt error is wrapped", func(t *testing.T) {
		original, hadOriginal := os.LookupEnv(AzDoEnvironmentOrgName)
		os.Unsetenv(AzDoEnvironmentOrgName)
		t.Cleanup(func() {
			if hadOriginal {
				os.Setenv(AzDoEnvironmentOrgName, original)
			} else {
				os.Unsetenv(AzDoEnvironmentOrgName)
			}
		})

		env := environment.NewWithValues("test", map[string]string{})
		mockConsole := mockinput.NewMockConsole()
		mockConsole.WhenPrompt(func(options input.ConsoleOptions) bool {
			return strings.Contains(options.Message, "Azure DevOps Organization")
		}).RespondFn(func(_ input.ConsoleOptions) (any, error) {
			return "", fmt.Errorf("user aborted")
		})

		mgr := &mockenv.MockEnvManager{}

		value, promptRequired, err := EnsureOrgNameExists(t.Context(), mgr, env, mockConsole)
		require.Error(t, err)
		assert.Empty(t, value)
		assert.False(t, promptRequired)
		assert.Contains(t, err.Error(), "asking for new project name")
		mgr.AssertNotCalled(t, "Save", mock.Anything, mock.Anything)
	})

	t.Run("save error is propagated", func(t *testing.T) {
		original, hadOriginal := os.LookupEnv(AzDoEnvironmentOrgName)
		os.Unsetenv(AzDoEnvironmentOrgName)
		t.Cleanup(func() {
			if hadOriginal {
				os.Setenv(AzDoEnvironmentOrgName, original)
			} else {
				os.Unsetenv(AzDoEnvironmentOrgName)
			}
		})

		env := environment.NewWithValues("test", map[string]string{})
		mockConsole := mockinput.NewMockConsole()
		mockConsole.WhenPrompt(func(options input.ConsoleOptions) bool {
			return strings.Contains(options.Message, "Azure DevOps Organization")
		}).Respond("new-org")

		mgr := &mockenv.MockEnvManager{}
		mgr.On("Save", mock.Anything, env).Return(fmt.Errorf("disk full"))

		value, promptRequired, err := EnsureOrgNameExists(t.Context(), mgr, env, mockConsole)
		require.Error(t, err)
		assert.Empty(t, value)
		assert.False(t, promptRequired)
		assert.Contains(t, err.Error(), "disk full")
	})
}

func TestSaveEnvironmentConfig(t *testing.T) {
	t.Run("writes value and calls save", func(t *testing.T) {
		env := environment.NewWithValues("test", map[string]string{})
		mgr := &mockenv.MockEnvManager{}
		mgr.On("Save", mock.Anything, env).Return(nil)

		err := saveEnvironmentConfig(t.Context(), "MY_KEY", "my-value", mgr, env)
		require.NoError(t, err)

		got, ok := env.Dotenv()["MY_KEY"]
		require.True(t, ok)
		assert.Equal(t, "my-value", got)
		mgr.AssertCalled(t, "Save", mock.Anything, env)
	})

	t.Run("propagates save error", func(t *testing.T) {
		env := environment.NewWithValues("test", map[string]string{})
		mgr := &mockenv.MockEnvManager{}
		mgr.On("Save", mock.Anything, env).Return(fmt.Errorf("boom"))

		err := saveEnvironmentConfig(t.Context(), "MY_KEY", "my-value", mgr, env)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "boom")
	})
}
