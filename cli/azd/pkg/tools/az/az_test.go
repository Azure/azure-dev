// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package az

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/stretchr/testify/require"
)

func TestAccount(t *testing.T) {
	t.Run("unauthenticated exit error with az login message", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		mockContext.CommandRunner.When(func(args exec.RunArgs, cmd string) bool {
			return strings.Contains(cmd, "az account show")
		}).SetError(fmt.Errorf(
			"exit code: 1, stdout: , stderr: ERROR: Please run 'az login' to setup account.",
		))

		azCli, err := NewCli(mockContext.CommandRunner)
		require.NoError(t, err)

		_, err = azCli.Account(context.Background())
		require.Error(t, err)
		require.Contains(t, err.Error(), "not authenticated")
	})

	t.Run("unauthenticated output with az login message", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		mockContext.CommandRunner.When(func(args exec.RunArgs, cmd string) bool {
			return strings.Contains(cmd, "az account show")
		}).Respond(exec.RunResult{
			Stderr: "ERROR: Please run 'az login' to setup account.",
		})

		azCli, err := NewCli(mockContext.CommandRunner)
		require.NoError(t, err)

		_, err = azCli.Account(context.Background())
		require.Error(t, err)
		require.Contains(t, err.Error(), "not authenticated")
	})

	t.Run("authenticated user account", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		mockContext.CommandRunner.When(func(args exec.RunArgs, cmd string) bool {
			return strings.Contains(cmd, "az account show")
		}).Respond(exec.RunResult{
			Stdout: `{"user": {"name": "test@example.com", "type": "user"}}`,
		})

		azCli, err := NewCli(mockContext.CommandRunner)
		require.NoError(t, err)

		account, err := azCli.Account(context.Background())
		require.NoError(t, err)
		require.Equal(t, "test@example.com", account.User.Name)
		require.Equal(t, "user", account.User.Type)
	})
}
