// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

const rleHelpFooter = `Environment & Project Context:
  Set AZD_AI_RLE_ENABLE=true in your shell to show the preview RLE commands.
  This switch accepts boolean values; commands are hidden by default.

  Set FOUNDRY_PROJECT_ENDPOINT in your shell to the Foundry project endpoint:
  https://<account>.services.ai.azure.com/api/projects/<project>
  Set AZURE_CONTAINER_REGISTRY_ENDPOINT to <registry>.azurecr.io for publish.
  These settings are read from the process environment, not directly from
  the named azd environment's .env file.

  An RLE environment is a runtime resource, not an azd deployment environment.
  Local RLE state is saved in .azd-rle.json. Show and invoke can use the saved
  environment name, or accept an existing environment name explicitly.
  Invoke accepts --version to choose a published version.

Learn More:
  Publish an environment: 'azd ai rle publish --help'
  Open a remote runtime shell: 'azd ai rle invoke --help'`
