// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmdsubst

import (
	"context"
	"errors"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/keyvault"
	"github.com/stretchr/testify/require"
)

// mockKeyVaultService implements keyvault.KeyVaultService
// for testing SecretOrRandomPasswordCommandExecutor.
type mockKeyVaultService struct {
	getSecretFn func(
		ctx context.Context,
		subscriptionId, vaultName, secretName string,
	) (*keyvault.Secret, error)
}

func (m *mockKeyVaultService) GetKeyVault(
	_ context.Context, _, _, _ string,
) (*keyvault.KeyVault, error) {
	return nil, nil
}

func (m *mockKeyVaultService) GetKeyVaultSecret(
	ctx context.Context,
	subscriptionId, vaultName, secretName string,
) (*keyvault.Secret, error) {
	return m.getSecretFn(
		ctx, subscriptionId, vaultName, secretName,
	)
}

func (m *mockKeyVaultService) PurgeKeyVault(
	_ context.Context, _, _, _ string,
) error {
	return nil
}

func (m *mockKeyVaultService) ListSubscriptionVaults(
	_ context.Context, _ string,
) ([]keyvault.Vault, error) {
	return nil, nil
}

func (m *mockKeyVaultService) CreateVault(
	_ context.Context, _, _, _, _, _ string,
) (keyvault.Vault, error) {
	return keyvault.Vault{}, nil
}

func (m *mockKeyVaultService) ListKeyVaultSecrets(
	_ context.Context, _, _ string,
) ([]string, error) {
	return nil, nil
}

func (m *mockKeyVaultService) CreateKeyVaultSecret(
	_ context.Context, _, _, _, _ string,
) error {
	return nil
}

func (m *mockKeyVaultService) SecretFromAkvs(
	_ context.Context, _ string,
) (string, error) {
	return "", nil
}

func Test_SecretOrRandomPassword_WrongCommand(t *testing.T) {
	svc := &mockKeyVaultService{}
	executor := NewSecretOrRandomPasswordExecutor(svc, "sub1")

	ran, result, err := executor.Run(
		context.Background(), "otherCommand", nil,
	)
	require.NoError(t, err)
	require.False(t, ran)
	require.Empty(t, result)
}

func Test_SecretOrRandomPassword_NoArgs(t *testing.T) {
	svc := &mockKeyVaultService{}
	executor := NewSecretOrRandomPasswordExecutor(svc, "sub1")

	ran, result, err := executor.Run(
		context.Background(),
		SecretOrRandomPasswordCommandName, nil,
	)
	require.NoError(t, err)
	require.True(t, ran)
	// Should generate a random password
	require.NotEmpty(t, result)
	require.GreaterOrEqual(t, len(result), 15)
}

func Test_SecretOrRandomPassword_OneArg(t *testing.T) {
	svc := &mockKeyVaultService{}
	executor := NewSecretOrRandomPasswordExecutor(svc, "sub1")

	ran, result, err := executor.Run(
		context.Background(),
		SecretOrRandomPasswordCommandName,
		[]string{"vaultOnly"},
	)
	require.NoError(t, err)
	require.True(t, ran)
	require.NotEmpty(t, result)
}

func Test_SecretOrRandomPassword_SecretFound(t *testing.T) {
	svc := &mockKeyVaultService{
		getSecretFn: func(
			_ context.Context, _, _, _ string,
		) (*keyvault.Secret, error) {
			return &keyvault.Secret{
				Value: "my-secret-value",
			}, nil
		},
	}
	executor := NewSecretOrRandomPasswordExecutor(svc, "sub1")

	ran, result, err := executor.Run(
		context.Background(),
		SecretOrRandomPasswordCommandName,
		[]string{"myVault", "mySecret"},
	)
	require.NoError(t, err)
	require.True(t, ran)
	require.Equal(t, "my-secret-value", result)
}

func Test_SecretOrRandomPassword_SecretNotFound(t *testing.T) {
	svc := &mockKeyVaultService{
		getSecretFn: func(
			_ context.Context, _, _, _ string,
		) (*keyvault.Secret, error) {
			return nil, keyvault.ErrAzCliSecretNotFound
		},
	}
	executor := NewSecretOrRandomPasswordExecutor(svc, "sub1")

	ran, result, err := executor.Run(
		context.Background(),
		SecretOrRandomPasswordCommandName,
		[]string{"myVault", "missingSecret"},
	)
	require.NoError(t, err)
	require.True(t, ran)
	// Falls back to random password
	require.NotEmpty(t, result)
}

func Test_SecretOrRandomPassword_EmptySecret(t *testing.T) {
	svc := &mockKeyVaultService{
		getSecretFn: func(
			_ context.Context, _, _, _ string,
		) (*keyvault.Secret, error) {
			return &keyvault.Secret{Value: ""}, nil
		},
	}
	executor := NewSecretOrRandomPasswordExecutor(svc, "sub1")

	ran, result, err := executor.Run(
		context.Background(),
		SecretOrRandomPasswordCommandName,
		[]string{"vault", "secret"},
	)
	require.NoError(t, err)
	require.True(t, ran)
	// Empty secret falls back to random password
	require.NotEmpty(t, result)
}

func Test_SecretOrRandomPassword_WhitespaceSecret(t *testing.T) {
	svc := &mockKeyVaultService{
		getSecretFn: func(
			_ context.Context, _, _, _ string,
		) (*keyvault.Secret, error) {
			return &keyvault.Secret{Value: "   "}, nil
		},
	}
	executor := NewSecretOrRandomPasswordExecutor(svc, "sub1")

	ran, result, err := executor.Run(
		context.Background(),
		SecretOrRandomPasswordCommandName,
		[]string{"vault", "secret"},
	)
	require.NoError(t, err)
	require.True(t, ran)
	// Whitespace-only secret falls back to random password
	require.NotEmpty(t, result)
}

func Test_SecretOrRandomPassword_VaultError(t *testing.T) {
	svc := &mockKeyVaultService{
		getSecretFn: func(
			_ context.Context, _, _, _ string,
		) (*keyvault.Secret, error) {
			return nil, errors.New("network error")
		},
	}
	executor := NewSecretOrRandomPasswordExecutor(svc, "sub1")

	ran, result, err := executor.Run(
		context.Background(),
		SecretOrRandomPasswordCommandName,
		[]string{"vault", "secret"},
	)
	require.Error(t, err)
	require.False(t, ran)
	require.Empty(t, result)
	require.Contains(t, err.Error(), "reading secret")
}

func Test_ContainsCommandInvocation_EmptyCommandName(
	t *testing.T,
) {
	require.False(t,
		ContainsCommandInvocation("$(cmd)", ""),
	)
}

func Test_ContainsCommandInvocation_EmptyBoth(t *testing.T) {
	require.False(t, ContainsCommandInvocation("", ""))
}

func Test_Eval_MultipleMixedCommands(t *testing.T) {
	// First command recognized, second unrecognized
	input := "a $(known x) b $(unknown y) c"
	expected := "a result b $(unknown y) c"

	result, err := Eval(
		context.Background(), input,
		testCommandExecutor{
			runImpl: func(
				name string, args []string,
			) (bool, string, error) {
				if name == "known" {
					return true, "result", nil
				}
				return false, "", nil
			},
		},
	)
	require.NoError(t, err)
	require.Equal(t, expected, result)
}

func Test_Eval_CommandWithNoArgs(t *testing.T) {
	input := "$(noargs)"
	result, err := Eval(
		context.Background(), input,
		testCommandExecutor{
			runImpl: func(
				name string, args []string,
			) (bool, string, error) {
				require.Equal(t, "noargs", name)
				require.Empty(t, args)
				return true, "done", nil
			},
		},
	)
	require.NoError(t, err)
	require.Equal(t, "done", result)
}

func Test_Eval_AdjacentSubstitutions(t *testing.T) {
	input := "$(a)$(b)"
	result, err := Eval(
		context.Background(), input,
		testCommandExecutor{
			runImpl: func(
				name string, _ []string,
			) (bool, string, error) {
				return true, name, nil
			},
		},
	)
	require.NoError(t, err)
	require.Equal(t, "ab", result)
}

func Test_Eval_ErrorOnFirstOfMultiple(t *testing.T) {
	input := "$(fail) $(ok)"
	_, err := Eval(
		context.Background(), input,
		testCommandExecutor{
			runImpl: func(
				name string, _ []string,
			) (bool, string, error) {
				if name == "fail" {
					return false, "",
						errors.New("first failed")
				}
				return true, "ok", nil
			},
		},
	)
	require.Error(t, err)
	require.Contains(t, err.Error(), "first failed")
}
