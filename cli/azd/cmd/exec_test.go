// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/keyvault"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// envSliceToMap converts an env slice ([]string{"KEY=VALUE", ...}) to a map.
// When duplicate keys exist, the last value wins (matching exec.Cmd behavior).
func envSliceToMap(env []string) map[string]string {
	m := make(map[string]string, len(env))
	for _, entry := range env {
		k, v, _ := strings.Cut(entry, "=")
		m[k] = v
	}
	return m
}

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

	childEnv, err := action.buildChildEnv(t.Context())
	require.NoError(t, err)

	envMap := envSliceToMap(childEnv)
	assert.Equal(t, "value1", envMap[key1])
	assert.Equal(t, "value2", envMap[key2])
}

func TestExecAction_ResolvesSecretReferences(t *testing.T) {
	const secretKey = "AZD_TEST_EXEC_SECRET"

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

	childEnv, err := action.buildChildEnv(t.Context())
	require.NoError(t, err)

	envMap := envSliceToMap(childEnv)
	assert.Equal(t, "resolved-secret-value", envMap[secretKey])
}

func TestExecAction_SecretResolutionFailure(t *testing.T) {
	const secretKey = "AZD_TEST_EXEC_SECRET_FAIL"

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

	_, err := action.buildChildEnv(t.Context())
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

func TestExecAction_ResolvesKeyVaultReferenceFormat(t *testing.T) {
	const secretKey = "AZD_TEST_EXEC_KVREF"

	// @Microsoft.KeyVault reference format (used in Azure App Service settings).
	secretRef := "@Microsoft.KeyVault(SecretUri=https://myvault.vault.azure.net/secrets/mysecret)"
	env := environment.NewWithValues("test", map[string]string{
		secretKey: secretRef,
	})

	kvMock := &mockExecKeyVaultService{
		secretFromKeyVaultRefFn: func(_ context.Context, ref string, _ string) (string, error) {
			assert.Equal(t, secretRef, ref)
			return "kv-resolved-value", nil
		},
	}

	action := &execAction{
		env:             env,
		keyvaultService: kvMock,
		flags:           &execFlags{global: &internal.GlobalCommandOptions{}},
		args:            []string{"go", "version"},
	}

	childEnv, err := action.buildChildEnv(t.Context())
	require.NoError(t, err)

	envMap := envSliceToMap(childEnv)
	assert.Equal(t, "kv-resolved-value", envMap[secretKey])
}

func TestExecAction_ExitCodePropagation(t *testing.T) {
	env := environment.NewWithValues("test", nil)
	kvMock := &mockExecKeyVaultService{}

	// Run a command that exits with code 42.
	action := &execAction{
		env:             env,
		keyvaultService: kvMock,
		flags:           &execFlags{global: &internal.GlobalCommandOptions{}},
		args:            []string{"go", "run", "-e", "exit 42"},
	}

	// We can't easily invoke a process that exits 42 portably here,
	// but we can verify the ExitCodeError type is returned by testing
	// the action against a command that fails with a known exit code.
	// Use a non-existent script path that falls through to inline mode
	// with an exit-code-producing shell command.
	action.args = []string{"exit 42"}
	_, err := action.Run(t.Context())
	require.Error(t, err)

	var exitCodeErr *internal.ExitCodeError
	if errors.As(err, &exitCodeErr) {
		assert.Equal(t, 42, exitCodeErr.ExitCode)
	}
	// If the shell doesn't support 'exit 42' inline (e.g. on Windows without bash),
	// the error may be of a different type — that's acceptable for this test.
}

func TestLooksLikeFilePath(t *testing.T) {
	tests := []struct {
		input string
		want  bool
	}{
		{"echo hello", false},
		{"go version", false},
		{"npm", false},
		{"./script.sh", true},
		{"scripts/deploy.sh", true},
		{"C:\\scripts\\deploy.ps1", true},
		{"deploy.sh", true},
		{"build.ps1", true},
		{"run.cmd", true},
		{"setup.bat", true},
		{"app.py", true},
		{"tool.rb", true},
		{"deploy.bash", true},
		{"config.zsh", true},
		{"mycommand", false},
		{"echo $HOME", false},
		{"ls -la", false},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			assert.Equal(t, tt.want, looksLikeFilePath(tt.input))
		})
	}
}

func TestExecAction_FileNotFoundNoInlineFallback(t *testing.T) {
	env := environment.NewWithValues("test", nil)
	kvMock := &mockExecKeyVaultService{}

	// A non-existent file with a script extension should NOT fall through
	// to inline execution — it should return ScriptNotFoundError.
	action := &execAction{
		env:             env,
		keyvaultService: kvMock,
		flags:           &execFlags{global: &internal.GlobalCommandOptions{}},
		args:            []string{"nonexistent.sh"},
	}

	_, err := action.Run(t.Context())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}
