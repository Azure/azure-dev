// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/keyvault"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockExecKeyVaultService implements keyvault.KeyVaultService for testing.
// Only SecretFromKeyVaultReference is wired; other methods panic if called.
type mockExecKeyVaultService struct {
	secretFromKeyVaultRefFn func(ctx context.Context, ref string, defaultSubID string) (string, error)
}

func (m *mockExecKeyVaultService) GetKeyVault(
	context.Context, string, string, string,
) (*keyvault.KeyVault, error) {
	panic("not implemented")
}

func (m *mockExecKeyVaultService) GetKeyVaultSecret(
	context.Context, string, string, string,
) (*keyvault.Secret, error) {
	panic("not implemented")
}

func (m *mockExecKeyVaultService) PurgeKeyVault(context.Context, string, string, string) error {
	panic("not implemented")
}

func (m *mockExecKeyVaultService) ListSubscriptionVaults(context.Context, string) ([]keyvault.Vault, error) {
	panic("not implemented")
}

func (m *mockExecKeyVaultService) CreateVault(
	context.Context, string, string, string, string, string,
) (keyvault.Vault, error) {
	panic("not implemented")
}

func (m *mockExecKeyVaultService) ListKeyVaultSecrets(context.Context, string, string) ([]string, error) {
	panic("not implemented")
}

func (m *mockExecKeyVaultService) CreateKeyVaultSecret(
	context.Context, string, string, string, string,
) error {
	panic("not implemented")
}

func (m *mockExecKeyVaultService) SecretFromAkvs(context.Context, string) (string, error) {
	panic("not implemented")
}

func (m *mockExecKeyVaultService) SecretFromKeyVaultReference(
	ctx context.Context, ref string, defaultSubID string,
) (string, error) {
	if m.secretFromKeyVaultRefFn != nil {
		return m.secretFromKeyVaultRefFn(ctx, ref, defaultSubID)
	}
	return "", errors.New("mockExecKeyVaultService: secretFromKeyVaultRefFn not set")
}

func TestExecAction_SetsEnvironmentVariables(t *testing.T) {
	const key1 = "AZD_TEST_EXEC_VAR1"
	const key2 = "AZD_TEST_EXEC_VAR2"

	t.Cleanup(func() {
		os.Unsetenv(key1)
		os.Unsetenv(key2)
	})

	env := environment.NewWithValues("test", map[string]string{
		key1: "value1",
		key2: "value2",
	})

	kvMock := &mockExecKeyVaultService{
		secretFromKeyVaultRefFn: func(_ context.Context, _ string, _ string) (string, error) {
			return "", errors.New("should not be called for plain values")
		},
	}

	action := &execAction{
		env:             env,
		keyvaultService: kvMock,
		flags:           &execFlags{global: &internal.GlobalCommandOptions{}},
		args:            []string{"go", "version"},
	}

	_, err := action.Run(t.Context())
	require.NoError(t, err)

	assert.Equal(t, "value1", os.Getenv(key1))
	assert.Equal(t, "value2", os.Getenv(key2))
}

func TestExecAction_ResolvesSecretReferences(t *testing.T) {
	const secretKey = "AZD_TEST_EXEC_SECRET"

	t.Cleanup(func() {
		os.Unsetenv(secretKey)
	})

	secretRef := "akvs://sub-id/vault-name/secret-name"
	env := environment.NewWithValues("test", map[string]string{
		secretKey: secretRef,
	})

	kvMock := &mockExecKeyVaultService{
		secretFromKeyVaultRefFn: func(_ context.Context, ref string, _ string) (string, error) {
			assert.Equal(t, secretRef, ref)
			return "resolved-secret-value", nil
		},
	}

	action := &execAction{
		env:             env,
		keyvaultService: kvMock,
		flags:           &execFlags{global: &internal.GlobalCommandOptions{}},
		args:            []string{"go", "version"},
	}

	_, err := action.Run(t.Context())
	require.NoError(t, err)

	assert.Equal(t, "resolved-secret-value", os.Getenv(secretKey))
}

func TestExecAction_SecretResolutionFailure(t *testing.T) {
	const secretKey = "AZD_TEST_EXEC_SECRET_FAIL"

	t.Cleanup(func() {
		os.Unsetenv(secretKey)
	})

	env := environment.NewWithValues("test", map[string]string{
		secretKey: "akvs://sub-id/vault-name/secret-name",
	})

	kvMock := &mockExecKeyVaultService{
		secretFromKeyVaultRefFn: func(_ context.Context, _ string, _ string) (string, error) {
			return "", errors.New("vault unavailable")
		},
	}

	action := &execAction{
		env:             env,
		keyvaultService: kvMock,
		flags:           &execFlags{global: &internal.GlobalCommandOptions{}},
		args:            []string{"go", "version"},
	}

	_, err := action.Run(t.Context())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "resolving secret")
}

func TestExecAction_InvalidShell(t *testing.T) {
	env := environment.NewWithValues("test", nil)

	kvMock := &mockExecKeyVaultService{}

	action := &execAction{
		env:             env,
		keyvaultService: kvMock,
		flags: &execFlags{
			global: &internal.GlobalCommandOptions{},
			shell:  "invalid-shell",
		},
		args: []string{"echo", "hello"},
	}

	_, err := action.Run(t.Context())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid configuration")
}

func TestExecAction_DirectExecMode(t *testing.T) {
	env := environment.NewWithValues("test", nil)

	kvMock := &mockExecKeyVaultService{}

	action := &execAction{
		env:             env,
		keyvaultService: kvMock,
		flags:           &execFlags{global: &internal.GlobalCommandOptions{}},
		args:            []string{"go", "version"},
	}

	_, err := action.Run(t.Context())
	require.NoError(t, err)
}

func TestNewExecCmd(t *testing.T) {
	cmd := newExecCmd()

	assert.Equal(t, "exec [command] [args...] [-- script-args...]", cmd.Use)
	assert.Contains(t, cmd.Short, "Execute commands")
	require.NotNil(t, cmd.Args)

	// Args validator requires at least 1 argument.
	assert.Error(t, cmd.Args(cmd, []string{}))
	assert.NoError(t, cmd.Args(cmd, []string{"echo"}))
	assert.NoError(t, cmd.Args(cmd, []string{"echo", "hello"}))

	// Verify SetInterspersed(false) was called by checking that the flags
	// set does not have interspersed enabled. pflag exposes HasFlags but
	// not interspersed directly; instead we verify the observable behavior
	// by confirming that the whitelist and arg validator work correctly.
	// The FParseErrWhitelist.UnknownFlags should be true.
	assert.True(t, cmd.FParseErrWhitelist.UnknownFlags,
		"unknown flags should be whitelisted so child flags are forwarded")
}
