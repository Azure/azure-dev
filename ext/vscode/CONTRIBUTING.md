# Contributing to VS Code extension for Azure Developer CLI

Welcome, and thank you for your interest in contributing to VS Code extension for Azure Developer CLI! 

We are open to all ideas and we want to get rid of bugs! Use [Discussions](https://aka.ms/azure-dev/discussions) to share new ideas or ask questions about Azure Developer CLI and the VS Code extension. To report problems use [Issues](https://aka.ms/azure-dev/issues).

## Building and installation

You need to have `Node.js` and `npm` installed. Node 16 (LTS) is recommended for development.

1.  **Build the VSIX file containing the extension**

    ```shell
    cd <Azure Developer CLI repository root>/ext/vscode
    npm install
    npm run package
    ``` 

2.  **Install the extension into VS Code**
    Open VS Code Extensions view, click on the "..." menu and choose "Install from VSIX...". Point to the VSIX file created in step 1.

You should now be able to execute Azure Dev commands e.g. "Azure Developer: Initialize a new application" etc.

## Debugging

To debug extension code use "Run extension" debug configuration, provided in the `launch.json` file in the repo. This configuration with (re)build the extension as necessary--no need to build anything manually.

## Using a custom version of `azd`

By default, the extension uses whatever version of the `azd` binary is present in your `$PATH`. If you are developing a feature a need to use a custom version of `azd`, You may set `AZURE_DEV_CLI_PATH` and the extension will use this version instead.

## Submitting a change

Before submitting a PR make sure that the unit tests pass (`npm run unit-test`) and that the code linter does not produce any errors or warnings (`npm run lint`). 

You might find [ESLint for Visual Studio Code](https://marketplace.visualstudio.com/items?itemName=dbaeumer.vscode-eslint) handy for keeping the code error-free as you are making changes.

## Legal

Before we can accept your pull request you will need to sign a **Contribution License Agreement**. All you need to do is to submit a pull request, then the PR will get appropriately labelled (e.g. `cla-required`, `cla-norequired`, `cla-signed`, `cla-already-signed`). If you already signed the agreement we will continue with reviewing the PR, otherwise system will tell you how you can sign the CLA. Once you sign the CLA all future PR's will be labeled as `cla-signed`.
