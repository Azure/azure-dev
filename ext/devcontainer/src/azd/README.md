# Azure Developer CLI (azd)

Installs the [Azure Developer CLI](https://github.com/Azure/azure-dev) along with needed dependencies.

## Example Usage

```json
"features": {
    "ghcr.io/azure/azure-dev/azd:1": {
        "version": "stable"
    }
}
```

## Options

| Options Id | Description | Type | Default Value |
|-----|-----|-----|-----|
| version | Select or enter an Azure Developer CLI version. (Available versions may vary by Linux distribution.) | string | stable |

## Customizations

### VS Code Extensions

- `ms-azuretools.azure-dev`