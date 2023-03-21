/*
 * Copyright (c) Microsoft Corporation. All rights reserved.
 * Licensed under the MIT License. See License.txt in the project root for license information.
 */

package com.microsoft.azure.simpletodo.configuration;

import com.azure.core.annotation.Immutable;
import com.azure.identity.ChainedTokenCredentialBuilder;
import com.azure.identity.DefaultAzureCredentialBuilder;
import com.azure.security.keyvault.secrets.SecretClientBuilder;

/**
 * A wrapper accessing configuration from external sources like Azure Key Vault.
 */
@Immutable
public class ConfigurationSources {

    /**
     * A source for getting configuration from Azure Key Vault Secrets
     */
    public class KeyVaultSecrets {

        // Create a key vault secret client to fetch the secret
        // Set azdTokenCredential as the first credential
        public static String get(String keyVaultUrl, String configName) {
            var credential = new ChainedTokenCredentialBuilder()
                .addFirst(new AzureDeveloperCliCredential())
                .addLast(new DefaultAzureCredentialBuilder().build())
                .build();
            var secretClient = new SecretClientBuilder().vaultUrl(keyVaultUrl).credential(credential).buildClient();
            var secret = secretClient.getSecret(configName);
            return secret.getValue();
        }
    }
}
