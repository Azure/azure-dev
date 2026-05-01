# Azure Logic Apps Standard extension

This azd extension makes it possible to package Logic Apps Standard projects and includes support for custom code projects.


## Installing

Assuming 'azd' is in your path, run the following commands to install the extension for the first time:

```shell
azd extension install azure.logicappsstandard
```

Or, if you already the `azure.logicappsstandard` extension installed, and you want to upgrade to the latest version:

```shell
azd extension upgrade azure.logicappsstandard
```

## Usage

This extension introduces the `logicappsstandard` language wich can package Logic Apps Standard projects.

For example, if your template has a Logic App Standard project with the following structure:

```
└── src
    └── logicApp
        ├── .vscode
        ├── Artifacts
        ├── lib
        ├── SampleWorkflow1
        │   └── workflow.json
        ├── SampleWorkflow2
        │   └── workflow.json
        ├── workflow-designtime
        ├── .funcignore
        ├── .gitignore
        ├── host.json
        └── local.settings.json
```

Use the following snippet in your `azure.yaml` file to configure the Logic App:

```yaml
services:
  logicApp:
    project: ./src/logicApp
    host: function
    language: logicappsstandard
```

This will package everything under the `src/logicApp` folder in a .zip file. Because `function` is used as the host, the exclusions in `.funcignore` are respected and only the relevant files are packaged.

The extension also supports Logic App Standard projects with a .NET 8 or .NET Framework custom code project. For example, if your template has a Logic App Standard project with custom code project following this structure:

```
└── src
    └── logicApp
        ├── Functions
        │   ├── MyFunctions.cs
        │   ├── Functions.csproj
        │   └── ...
        └── Workflows
            ├── SampleWorkflow1
            │   └── workflow.json
            ├── SampleWorkflow1
            │   └── workflow.json
            ├── host.json
            └── ...
```

You can use the following snippet in your `azure.yaml` file to configure the Logic App:

```yaml
services:
  logicAppSample2:
    project: ./src/logicApp
    dist: Workflows
    host: function
    language: logicappsstandard
    customCodeProject: Functions/Functions.csproj
```

This will first build the custom code project and then package the Logic App Standard artifacts.

- The `project` property contains the rootfolder of the Logic App Standard project.
- The `dist` property is the relative path to the folder with the Logic App Standard files that will be packaged.
- The `customCodeProject` property is the path to the custom code project's `.csproj` file.


## Local Development

### Prerequisites

1. **Install developer kit extension** (if not already installed):
   ```bash
   azd ext install microsoft.azd.extensions
   ```

   > **Note**: If you encounter an error about the extension not being in the registry, verify you have the default source configured:
   > ```bash
   > azd ext source list
   > ```
   > If missing, add it:
   > ```bash
   > azd ext source add -n azd -t url -l "https://aka.ms/azd/extensions/registry"
   > ```

### Building and Installing

1. **Navigate to the extension directory**:
   ```bash
   cd cli/azd/extensions/azure.logicappsstandard
   ```

2. **Initial setup** (first time only):
   ```bash
   azd x build
   azd x pack
   azd x publish
   ```

3. **Install the extension**:
   ```bash
   azd ext install azure.logicappsstandard
   ```

4. **For subsequent development** (after initial setup):
   ```bash
   azd x watch
   ```
   This automatically watches for file changes, rebuilds, and installs updates locally.

   Or for manual builds:
   ```bash
   azd x build
   ```
   This builds and automatically installs the updated extension.

> [!NOTE]
> The `pack` and `publish` steps are only required for the first time setup. For ongoing development, `azd x watch` or `azd x build` handles all updates automatically.
