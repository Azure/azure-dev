# Azure Developer CLI VS Code Extension Commands

This document describes utility commands provided by the Azure Developer CLI Visual Studio Code extension.

## Utility Commands

The following utility commands are provided for use in VS Code configurations but are not directly available in the command palette.

### azure-dev.commands.getDotEnvFilePath

This command retrieves the path to the `.env` file for a specified Azure Developer CLI environment or for the default environment if none is specified. This is useful for VS Code configurations where you need to access environment variables from an Azure Developer CLI environment.

#### Usage in launch.json

One common use case is to configure a debug session to use environment variables from an Azure Developer CLI environment's `.env` file. To do this, add the following to your `launch.json` file:

```json
{
  "configurations": [
    {
      // Your debug configuration
      "envFile": "${input:dotEnvFilePath}"
    }
  ],
  "inputs": [
    {
      "id": "dotEnvFilePath",
      "type": "command",
      "command": "azure-dev.commands.getDotEnvFilePath"
    }
  ]
}
```

This configuration will use the `.env` file from the default Azure Developer CLI environment. If you want to use a specific environment, you can pass its name as an argument:

```json
{
  "inputs": [
    {
      "id": "dotEnvFilePath",
      "type": "command",
      "command": "azure-dev.commands.getDotEnvFilePath",
      "args": ["my-environment-name"]
    }
  ]
}
```

#### Parameters

The command accepts the following optional parameters as an array:

1. `environmentName` (optional): The name of the environment to use. If not provided, the default environment will be used.
2. `workingDir` (optional): The working directory to use when looking for environments. If not provided, the first workspace folder will be used.

#### Error Handling

The command will throw an error if:

- No working directory can be determined
- No Azure Developer environments are found
- The specified environment does not exist
- There is no default environment when none is specified

These errors will be shown to the user when the debug session starts or when the command is executed.