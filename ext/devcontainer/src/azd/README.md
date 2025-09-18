# Azure Developer CLI (azd)

Installs the [Azure Developer CLI](https://github.com/Azure/azure-dev) along with needed dependencies.

## Example Usage - Install latest `azd` version

```json
"features": {
    "ghcr.io/azure/azure-dev/azd:latest": {}
}
```

## Example Usage - Install a specific `azd` version

Select a specific `azd` version [here](https://github.com/Azure/azure-dev/releases) and use it in `version`.

```json
"features": {
    "ghcr.io/azure/azure-dev/azd:latest": {
        "version": "<version-number>"
    }
},
```

## Options

| Options Id | Description | Type | Default Value |
|-----|-----|-----|-----|
| version | Select or enter an Azure Developer CLI version. (Available versions may vary by Linux distribution.) | string | stable |

## Customizations

### VS Code Extensions

- `ms-azuretools.azure-dev`