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

The Azure Developer CLI can take care of all the heavy lifting of setting up continuous integration for your application. Either if you are using GitHub or Azure DevOps, you can run `azd pipeline config` and let AZD to guide you thru simple steps to set CI/CD.
As part of the automatic settings, AZD creates secrets and variables for your CI/CD deployment workflow, for example, the Azure Subscription Id a location are set as variables.
Additionally, you can define a list of variables and secrets by using the `pipeline` configuration in the `azure.yaml` file from your project. The list of variables or secrets you define there corresponds to the name of the keys in your AZD environment (.env). If the name of the key is holding a secret reference (akvs), AZD will follow the following rules to apply the value to your CI/CD settings.

### Variables

If the secret is added to the `variables` section of the pipeline config, AZD will use the value from the environment without pulling the value for the secret. You can use this approach when you want to keep the Azure Key Vault references as part of your CI/CD settings. You can then rotate your secrets in your Key Vault and have your CI/CD to use the latest value without updating your workflow variables or secrets.

#### Example

- From an initialized AZD project, you have already set a secret. For example, you ran:

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

- When you run `azd pipeline config`, the `SECURE_KEY` will be set as a variable in your CI/CD workflow and its value will be the Azure Key Vault reference. For the example above, it would be: `akvs://faa080af-c1d8-40ad-9cce-000000000000/vivazqu-kv/SECURE-KEY-kv-secret`. 

> AZD will try to assign read-access role to the service principal that is used for logging in AZD from the CI/CD workflow. If you don't have enough access to assign the read role for the Key Vault, the operation will fail.

- Now you can reference the `SECURE_KEY` variable as a system environment. If `SECURE_KEY` is mapped to a bicep input parameter or if it is mapped from a hook definition (see the top of this file to learn about that), AZD will automatically get the value for the secret.

### Secrets

If the secret is added to the `secrets` section of the pipeline config, AZD will use the value of the secret by first reading it from the Key Vault. You can use this approach when you can't assign a read role to the service principal you are using on your CI/CD workflow. However, you will need to update the value every time you rotate the secret. You can run `azd pipeline config` again to re-apply the value.

#### Example

- From an initialized AZD project, you have already set a secret. For example, you ran:

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
