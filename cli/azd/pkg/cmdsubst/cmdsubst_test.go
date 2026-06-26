// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmdsubst

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/azure/azure-dev/cli/azd/pkg/keyvault"
)

type testCommandExecutor struct {
	runImpl func(name string, args []string) (bool, string, error)
}

func (tc testCommandExecutor) Run(ctx context.Context, name string, args []string) (bool, string, error) {
	return tc.runImpl(name, args)
}

func TestEvalWorksWithEmptyInput(t *testing.T) {
	evaluatorCalled := false
	result, err := Eval(t.Context(), "", testCommandExecutor{
		runImpl: func(name string, args []string) (bool, string, error) {
			evaluatorCalled = true
			return false, "", nil
		},
	})

	require.NoError(t, err)
	require.Equal(t, "", result)
	require.False(t, evaluatorCalled)
}

func TestEmptyInvocation(t *testing.T) {
	// This is not a valid command, so it should be left alone
	evaluatorCalled := false
	result, err := Eval(t.Context(), " $()  ", testCommandExecutor{
		runImpl: func(name string, args []string) (bool, string, error) {
			evaluatorCalled = true
			return false, "", nil
		},
	})

	require.NoError(t, err)
	require.Equal(t, " $()  ", result)
	require.False(t, evaluatorCalled)
}

func TestEvalNoSubstitution(t *testing.T) {
	const input = `{
		"$schema": "https://schema.management.azure.com/schemas/2019-04-01/deploymentParameters.json#",
		"contentVersion": "1.0.0.0",
		"parameters": {
		  "name": {
			"value": "${AZURE_ENV_NAME}"
		  },
		  "location": {
			"value": "${AZURE_LOCATION}"
		  },
		  "principalId": {
			"value": "${AZURE_PRINCIPAL_ID}"
		  }
		}
	  }`

	evaluatorCalled := false
	result, err := Eval(t.Context(), input, testCommandExecutor{
		runImpl: func(name string, args []string) (bool, string, error) {
			evaluatorCalled = true
			return false, "", nil
		},
	})

	require.NoError(t, err)
	require.Equal(t, input, result)
	require.False(t, evaluatorCalled)
}

func TestSubstitution(t *testing.T) {
	const input = `{
		"$schema": "https://schema.management.azure.com/schemas/2019-04-01/deploymentParameters.json#",
		"contentVersion": "1.0.0.0",
		"parameters": {
		  "name": {
			"value": "${AZURE_ENV_NAME}"
		  },
		  "password": {
			"value": "$(randomPassword)"
		  } 
		}
	  }`
	const expected = `{
		"$schema": "https://schema.management.azure.com/schemas/2019-04-01/deploymentParameters.json#",
		"contentVersion": "1.0.0.0",
		"parameters": {
		  "name": {
			"value": "${AZURE_ENV_NAME}"
		  },
		  "password": {
			"value": "very-secret"
		  } 
		}
	  }`

	result, err := Eval(t.Context(), input, testCommandExecutor{
		runImpl: func(name string, args []string) (bool, string, error) {
			if name == "randomPassword" {
				return true, "very-secret", nil
			} else {
				return false, "", nil
			}
		},
	})

	require.NoError(t, err)
	require.Equal(t, expected, result)
}

func TestEvaluatorReportingUnknownCommand(t *testing.T) {
	const input = `{
		"$schema": "https://schema.management.azure.com/schemas/2019-04-01/deploymentParameters.json#",
		"contentVersion": "1.0.0.0",
		"parameters": {
		  "name": {
			"value": "${AZURE_ENV_NAME}"
		  },
		  "password": {
			"value": "$(unknownCmd)"
		  } 
		}
	  }`

	evaluatorCalled := false
	result, err := Eval(t.Context(), input, testCommandExecutor{
		runImpl: func(name string, args []string) (bool, string, error) {
			evaluatorCalled = true
			return false, "", nil
		},
	})

	require.NoError(t, err)
	require.Equal(t, input, result) // No substitution, input preserved
	require.True(t, evaluatorCalled)
}

func TestEvaluatorError(t *testing.T) {
	const input = `{
		"$schema": "https://schema.management.azure.com/schemas/2019-04-01/deploymentParameters.json#",
		"contentVersion": "1.0.0.0",
		"parameters": {
		  "name": {
			"value": "${AZURE_ENV_NAME}"
		  },
		  "password": {
			"value": "$(causesError 1 alpha)"
		  } 
		}
	  }`

	evaluatorErr := fmt.Errorf("Something bad happened")
	_, err := Eval(t.Context(), input, testCommandExecutor{
		runImpl: func(name string, args []string) (bool, string, error) {
			return false, "", evaluatorErr
		},
	})

	require.Error(t, err)
	require.Equal(t, evaluatorErr, err)
}

func TestParameterExtraction(t *testing.T) {
	const input = `{
		"$schema": "https://schema.management.azure.com/schemas/2019-04-01/deploymentParameters.json#",
		"contentVersion": "1.0.0.0",
		"parameters": {
		  "name": {
			"value": "${AZURE_ENV_NAME}"
		  },
		  "password": {
			"value": "$( randomPassword alpha  bravo 17 foobar-1)"
		  } 
		}
	  }`

	const expected = `{
		"$schema": "https://schema.management.azure.com/schemas/2019-04-01/deploymentParameters.json#",
		"contentVersion": "1.0.0.0",
		"parameters": {
		  "name": {
			"value": "${AZURE_ENV_NAME}"
		  },
		  "password": {
			"value": "randomPassword called with [alpha bravo 17 foobar-1]"
		  } 
		}
	  }`

	result, err := Eval(t.Context(), input, testCommandExecutor{
		runImpl: func(name string, args []string) (bool, string, error) {
			if name == "randomPassword" {
				return true, fmt.Sprintf("%s called with %v", name, args), nil
			} else {
				return false, "", fmt.Errorf("Unknown command '%s', should not happen", name)
			}
		},
	})

	require.NoError(t, err)
	require.Equal(t, expected, result)
}

func TestMultipleSubstitutions(t *testing.T) {
	const input = `$(say alpha maybe) bravo $(say charlie)
	$(say delta) echo
	$(say foxtrot) golf $(say hotel for sure)`

	const expected = `alpha bravo charlie
	delta echo
	foxtrot golf hotel`

	result, err := Eval(t.Context(), input, testCommandExecutor{
		runImpl: func(name string, args []string) (bool, string, error) {
			if name == "say" {
				return true, args[0], nil
			} else {
				return false, "", fmt.Errorf("Unknown command '%s', should not happen", name)
			}
		},
	})

	require.NoError(t, err)
	require.Equal(t, expected, result)
}

func TestFullSubstitution(t *testing.T) {
	cmd := testCommandExecutor{
		runImpl: func(name string, args []string) (bool, string, error) {
			if name == "say" {
				return true, args[0], nil
			} else {
				return false, "", fmt.Errorf("Unknown command '%s', should not happen", name)
			}
		},
	}

	// The whole input needs to be substituted
	var input = `$(say alpha)`
	var expected = `alpha`
	result, err := Eval(t.Context(), input, cmd)
	require.NoError(t, err)
	require.Equal(t, expected, result)

	// Special case that is important for us
	input = `"$(say alpha)"`
	expected = `"alpha"`
	result, err = Eval(t.Context(), input, cmd)
	require.NoError(t, err)
	require.Equal(t, expected, result)

	// Once more, with whitespace, which should be preserved
	input = ` " $(say alpha)"  `
	expected = ` " alpha"  `
	result, err = Eval(t.Context(), input, cmd)
	require.NoError(t, err)
	require.Equal(t, expected, result)
}

func TestCommandContainsInvocation(t *testing.T) {
	// Empty doc
	var input = ""
	require.False(t, ContainsCommandInvocation(input, "cmd"))

	// No commands
	input = "alpha bravo charlie"
	require.False(t, ContainsCommandInvocation(input, "cmd"))

	// Empty command (invalid invocation)
	input = "alpha bravo $() charlie"
	require.False(t, ContainsCommandInvocation(input, "cmd"))

	// Invocation, but different command
	input = "alpha $(otherCmd) charlie"
	require.False(t, ContainsCommandInvocation(input, "Cmd"))

	// Invocation at the beginning
	input = "$(cmd)alpha bravo charlie"
	require.True(t, ContainsCommandInvocation(input, "cmd"))

	// Invocation at the end
	input = "alpha bravo charlie$(cmd)"
	require.True(t, ContainsCommandInvocation(input, "cmd"))

	// Invocation in the middle
	input = "alpha bravo$(cmd) charlie"
	require.True(t, ContainsCommandInvocation(input, "cmd"))

	// Multiple invocations
	input = "alpha $(cmd) bravo charlie$(cmd)"
	require.True(t, ContainsCommandInvocation(input, "cmd"))

	// Parameters with dash characters
	input = "$(cmd foo-1 foo-2)"
	require.True(t, ContainsCommandInvocation(input, "cmd"))
}

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

func (m *mockKeyVaultService) SecretFromKeyVaultReference(
	_ context.Context, _ string, _ string,
) (string, error) {
	return "", nil
}

func Test_SecretOrRandomPassword_WrongCommand(t *testing.T) {
	svc := &mockKeyVaultService{}
	executor := NewSecretOrRandomPasswordExecutor(svc, "sub1")

	ran, result, err := executor.Run(
		t.Context(), "otherCommand", nil,
	)
	require.NoError(t, err)
	require.False(t, ran)
	require.Empty(t, result)
}

func Test_SecretOrRandomPassword_NoArgs(t *testing.T) {
	svc := &mockKeyVaultService{}
	executor := NewSecretOrRandomPasswordExecutor(svc, "sub1")

	ran, result, err := executor.Run(
		t.Context(),
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
		t.Context(),
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
		t.Context(),
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
		t.Context(),
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
		t.Context(),
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
		t.Context(),
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
		t.Context(),
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
		t.Context(), input,
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
		t.Context(), input,
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
		t.Context(), input,
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
		t.Context(), input,
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
