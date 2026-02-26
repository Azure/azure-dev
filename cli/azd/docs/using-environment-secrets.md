# Using Environment Secrets

The Azure Developer CLI has introduced the capability to reference Azure Key Vault secrets within your project environment (the .env file). This functionality has been available since version 1.12.

To set a Key Vault secret, execute the command `azd env set-secret <name>`, where the `name` argument represents the key in the environment that references the Key Vault secret. Subsequently, the Azure Developer CLI will automatically retrieve the value from the Key Vault secret in the specified use cases.

## Bicep Parameters

To associate a Bicep parameter with the value of an Azure Key Vault secret, please follow these steps:

1. Annotate the Bicep parameter as secured by using the `@secure()` keyword.
2. Establish a mapping in the `main.parameters.json` file, linking the Bicep parameter to the corresponding key name in the environment that references the Azure Key Vault secret.

In scenarios where the key's value is a reference to an Azure Key Vault secret and the parameter is designated as secured, the Azure Developer CLI will attempt to retrieve the Key Vault secret value and apply it to the parameter.

Please note:
- The use of environment secrets is not currently supported when utilizing Bicep parameter files (`.bicepparam`).

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


## Pipeline config

The Azure Developer CLI simplifies the process of setting up continuous integration (CI) for your application. Whether you are using GitHub or Azure DevOps, you can run the command `azd pipeline config` and follow the guided steps provided by azd to configure CI/CD.

As part of the automatic configuration, azd creates secrets and variables for your CI/CD deployment workflow. For example, the Azure Subscription ID and location are set as variables. Additionally, you can define a list of variables and secrets by using the `pipeline` configuration in the `azure.yaml` file within your project. The list of variables or secrets you define corresponds to the names of the keys in your azd environment (.env). If the name of the key holds a secret reference (akvs), azd will apply the following rules to set the value in your CI/CD settings.

### Variables

If the secret is added to the `variables` section of the pipeline configuration, the Azure Developer CLI (azd) will use the value from the environment without retrieving the actual secret value. This approach is beneficial when you prefer to maintain Azure Key Vault references within your CI/CD settings. By doing so, you can rotate your secrets in the Key Vault, ensuring that your CI/CD pipeline uses the latest secret values without the need to update your workflow variables or secrets.

#### Example

- From an initialized azd project, you have already set a secret. For example, you ran:

```sh
azd env set-secret SECURE_KEY
```

If you run `azd env get-values`, you would see an output like:

```
AZURE_ENV_NAME="your-env-name"
AZURE_LOCATION="location"
AZURE_SUBSCRIPTION_ID="faa080af-c1d8-40ad-9cce-000000000000"
SECURE_KEY="akvs://faa080af-c1d8-40ad-9cce-000000000000/vivazqu-kv/SECURE-KEY-kv-secret"
```

- Then, you can set `SECURE_KEY` as a `variable` for your CI/CD pipeline by adding the `pipeline` field to the `azure.yaml` like:

```yaml
# yaml-language-server: $schema=https://raw.githubusercontent.com/Azure/azure-dev/main/schemas/v1.0/azure.yaml.json
name: your-project-name
pipeline:
  variables:
    - SECURE_KEY
```
When you run `azd pipeline config`, the `SECURE_KEY` will be set as a variable in your CI/CD workflow, and its value will be the Azure Key Vault reference. For the example above, it would be: `akvs://faa080af-c1d8-40ad-9cce-000000000000/vivazqu-kv/SECURE-KEY-kv-secret`.

> Note: azd will attempt to assign a read-access role to the service principal used for logging into azd from the CI/CD workflow. If you do not have sufficient permissions to assign the read role for the Key Vault, the operation will fail.

You can reference the `SECURE_KEY` variable as a system environment variable. If `SECURE_KEY` is mapped to a Bicep input parameter or if it is mapped from a hook definition (refer to the earlier sections of this document for more details), azd will automatically retrieve the value for the secret.

### Secrets

If the secret is added to the `secrets` section of the pipeline configuration, the Azure Developer CLI (azd) will retrieve the actual value of the secret from the Azure Key Vault. This method is useful when it is not possible to assign a read-access role to the service principal used in your CI/CD workflow. However, it is important to note that you will need to update the secret value manually each time the secret is rotated. To re-apply the updated value, you can run the `azd pipeline config` command again.

#### Example

- From an initialized azd project, you have already set a secret. For example, you ran:

```sh
azd env set-secret SECURE_KEY
```

If you run `azd env get-values`, you would see an output like:

```
AZURE_ENV_NAME="your-env-name"
AZURE_LOCATION="location"
AZURE_SUBSCRIPTION_ID="faa080af-c1d8-40ad-9cce-000000000000"
SECURE_KEY="akvs://faa080af-c1d8-40ad-9cce-000000000000/vivazqu-kv/SECURE-KEY-kv-secret"
```

- Then, you can set `SECURE_KEY` as a `secret` for your CI/CD pipeline by adding the `pipeline` field to the `azure.yaml` like:

```yaml
# yaml-language-server: $schema=https://raw.githubusercontent.com/Azure/azure-dev/main/schemas/v1.0/azure.yaml.json
name: your-project-name
pipeline:
  secrets:
    - SECURE_KEY
```

- When you run `azd pipeline config`, the `SECURE_KEY` will be set as a secret in your CI/CD workflow and its value will be the Azure Key Vault value.
