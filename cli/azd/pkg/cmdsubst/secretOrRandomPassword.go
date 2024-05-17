// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmdsubst

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/azure/azure-dev/cli/azd/pkg/keyvault"
	"github.com/azure/azure-dev/cli/azd/pkg/password"
)

const SecretOrRandomPasswordCommandName string = "secretOrRandomPassword"

type SecretOrRandomPasswordCommandExecutor struct {
	keyvaultService keyvault.KeyVaultService
	subscriptionId  string
}

func NewSecretOrRandomPasswordExecutor(
	keyvaultService keyvault.KeyVaultService, subscriptionId string) *SecretOrRandomPasswordCommandExecutor {
	return &SecretOrRandomPasswordCommandExecutor{
		keyvaultService: keyvaultService,
		subscriptionId:  subscriptionId,
	}
}

func (e *SecretOrRandomPasswordCommandExecutor) Run(
	ctx context.Context,
	commandName string,
	args []string,
) (bool, string, error) {
	if commandName != SecretOrRandomPasswordCommandName {
		return false, "", nil
	}

	generatePassword := func() (bool, string, error) {
		substitute, err := password.Generate(
			password.GenerateConfig{MinLower: to.Ptr[uint](5), MinUpper: to.Ptr[uint](5), MinNumeric: to.Ptr[uint](5)})
		return err == nil, substitute, err
	}

	// We expect two arguments: the KeyVault name and the secret name
	// If any is missing, we assume it is a "keyvault does not exist" case and fall back to random password generation.
	if len(args) != 2 {
		return generatePassword()
	}
	keyVaultName := args[0]
	secretName := args[1]

	secret, err := e.keyvaultService.GetKeyVaultSecret(ctx, e.subscriptionId, keyVaultName, secretName)
	if err != nil {
		if errors.Is(err, keyvault.ErrAzCliSecretNotFound) {
			log.Printf(
				"%s: secret '%s' not found in vault '%s', using random password...",
				SecretOrRandomPasswordCommandName,
				secretName,
				keyVaultName,
			)
			return generatePassword()
		} else {
			return false, "", fmt.Errorf("reading secret '%s' from vault '%s': %w", secretName, keyVaultName, err)
		}
	}

	if len(strings.TrimSpace(secret.Value)) == 0 {
		log.Printf(
			"%s: secret '%s' in vault '%s' has empty value, using random password...",
			SecretOrRandomPasswordCommandName,
			secretName,
			keyVaultName,
		)
		return generatePassword() // Do not use empty password secret even if the secret exists
	}

	return true, secret.Value, nil
}
