// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmdsubst

import (
	"context"
	"errors"
	"fmt"

	"github.com/azure/azure-dev/cli/azd/pkg/password"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/azcli"
)

const COMMAND_NAME string = "secretOrRandomPassword"

type SecretOrRandomPasswordCommandExecutor struct {
	ctx            context.Context
	azCli          azcli.AzCli
	vaults         []azcli.AzCliKeyVault
	subscriptionId string
}

func (e *SecretOrRandomPasswordCommandExecutor) SetRunContext(ctx context.Context, azCli azcli.AzCli, vaults []azcli.AzCliKeyVault, subscriptionId string) {
	e.ctx = ctx
	e.azCli = azCli
	e.vaults = vaults
	e.subscriptionId = subscriptionId
}

func (e *SecretOrRandomPasswordCommandExecutor) ContainsCommandInvocation(doc string) bool {
	return ContainsCommandInvocation(doc, COMMAND_NAME)
}

func (e SecretOrRandomPasswordCommandExecutor) Run(commandName string, args []string) (bool, string, error) {
	if commandName != COMMAND_NAME {
		return false, "", nil
	}

	generatePassword := func() (bool, string, error) {
		substitute, err := password.Generate(password.PasswordComposition{NumLowercase: 5, NumUppercase: 5, NumDigits: 5})
		return err == nil, substitute, err
	}

	// We expect two arguments: the KeyVault name and the secret name
	// If any is missing, we assume it is a "keyvault does not exist" case and fall back to random password generation.
	if len(args) != 2 {
		return generatePassword()
	}

	keyVaultName := args[0]
	secretName := args[1]
	if e.ctx == nil || e.azCli == nil || len(e.subscriptionId) == 0 {
		// Should never happen really...
		return false, "", fmt.Errorf("context not set for executing %s command", COMMAND_NAME)
	}

	if len(e.vaults) == 0 {
		return generatePassword()
	}

	for _, v := range e.vaults {
		if v.Name != keyVaultName {
			continue
		}

		secret, err := e.azCli.GetKeyVaultSecret(e.ctx, e.subscriptionId, keyVaultName, secretName)
		if err != nil {
			if errors.Is(err, azcli.ErrAzCliSecretNotFound) {
				continue
			} else {
				return false, "", fmt.Errorf("reading secret '%s' from vault '%s': %w", secretName, keyVaultName, err)
			}
		} else if len(secret.Value) > 0 {
			return true, secret.Value, nil
		} else {
			continue // Do not use empty password secret even if the secret exists
		}
	}

	return generatePassword()
}
