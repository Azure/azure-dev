# Using Environment Secrets

The Azure Developer CLI has introduced the capability to reference Azure Key Vault secrets within your project environment (the .env file). This functionality has been available since version 1.12.

To set a Key Vault secret, execute the command `azd env set-secret <name>`, where the `name` argument represents the key in the environment that references the Key Vault secret. Subsequently, the Azure Developer CLI will automatically retrieve the value from the Key Vault secret in the specified use cases.

## Bicep Parameters

To associate a Bicep parameter with the value of an Azure Key Vault secret, please follow these steps:

1. Annotate the Bicep parameter as secured by using the `@secure()` keyword.
2. Establish a mapping in the `main.parameters.json` file, linking the Bicep parameter to the corresponding key name in the environment that references the Azure Key Vault secret.

In scenarios where the key's value is a reference to an Azure Key Vault secret and the parameter is designated as secured, the Azure Developer CLI will attempt to retrieve the Key Vault secret value and apply it to the parameter.

Please note:
- The use of environment secrets is not currently supported when utilizing Bicep parameter files.

### Example

From an Azure Developer CLI project, execute the command `azd env set-secret MY_SECRET` and follow the instructions to either select an existing Azure Key Vault secret or create a new one. Upon completion of the command, the key `MY_SECRET` will hold a reference to a Key Vault secret. Then, add or select the Bicep parameter that you wish to use the value with and designate it as secure.

```bicep
@secure()
param secureParameter string
```

Finally, create the mapping in the `main.parameters.json` file.

```json
{
    "$schema": "https://schema.management.azure.com/schemas/2019-04-01/deploymentParameters.json#",
    "contentVersion": "1.0.0.0",
    "parameters": {
      "secureParameter": {
        "value": "${MY_SECRET}"
      }
    }
}
```

The next time you execute `azd up` or `azd provision`, the Azure Developer CLI will use the Azure Key Vault secret as the value for your parameter.

## Hooks

The Azure Developer CLI provides the capability to automatically retrieve Azure Key Vault secrets when a hook is executed. By default, when the Azure Developer CLI runs a hook, all key-value pairs from the project environment (the .env file) are set in the hook's environment. However, references to Key Vault secrets are not automatically resolved to their corresponding secret values.

To resolve these references, follow these steps:

- Establish a mapping from the key in the environment to a new key where the Key Vault secret is resolved.
- Utilize the `secrets` field in the hook definition to create this mapping.

### Example

Within an Azure Developer CLI project, execute the command `azd env set-secret MY_SECRET` and follow the instructions to either select an existing Azure Key Vault secret or create a new one. Upon completion of the command, the key `MY_SECRET` will reference a Key Vault secret. Then, create a hook definition appropriate for your operating system, as shown below:

For Linux:

```yaml
hooks:
  preprovision: 
    run: 'echo ".env value: $MY_SECRET \nResolved secret: $SECRET_RESOLVE"'
    shell: sh
    interactive: true
    secrets:
      SECRET_RESOLVE: MY_SECRET
```

For Windows:

```yaml
hooks:
  preprovision: 
    run: 'Write-Host ".env value: $env:MY_SECRET `nResolved secret: $env:SECRET_RESOLVE"'
    shell: pwsh
    interactive: true
    secrets:
      SECRET_RESOLVE: MY_SECRET
```

The next time you execute `azd provision`, the `preprovision` hook will run and resolve `MY_SECRET` into `SECRET_RESOLVE`.
