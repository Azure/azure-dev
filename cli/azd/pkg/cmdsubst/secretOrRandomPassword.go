// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmdsubst

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/password"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/azcli"
)

const SecretOrRandomPasswordCommandName string = "secretOrRandomPassword"

type SecretOrRandomPasswordCommandExecutor struct {
	ctx            context.Context
	azCli          azcli.AzCli
	subscriptionId string
}

func NewSecretOrRandomPasswordExecutor(ctx context.Context, azCli azcli.AzCli, subscriptionId string) SecretOrRandomPasswordCommandExecutor {
	return SecretOrRandomPasswordCommandExecutor{
		ctx:            ctx,
		azCli:          azCli,
		subscriptionId: subscriptionId,
	}
}

func (e SecretOrRandomPasswordCommandExecutor) Run(commandName string, args []string) (bool, string, error) {
	if commandName != SecretOrRandomPasswordCommandName {
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
		return false, "", fmt.Errorf("missing context information for %s command", SecretOrRandomPasswordCommandName)
	}

	secret, err := e.azCli.GetKeyVaultSecret(e.ctx, e.subscriptionId, keyVaultName, secretName)
	if err != nil {
		if errors.Is(err, azcli.ErrAzCliSecretNotFound) {
			log.Printf("%s: secret '%s' not found in vault '%s', using random password...", SecretOrRandomPasswordCommandName, secretName, keyVaultName)
			return generatePassword()
		} else {
			return false, "", fmt.Errorf("reading secret '%s' from vault '%s': %w", secretName, keyVaultName, err)
		}
	}

	if len(strings.TrimSpace(secret.Value)) == 0 {
		log.Printf("%s: secret '%s' in vault '%s' has empty value, using random password...", SecretOrRandomPasswordCommandName, secretName, keyVaultName)
		return generatePassword() // Do not use empty password secret even if the secret exists
	}

	return true, secret.Value, nil
}
