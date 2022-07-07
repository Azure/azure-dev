// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

import { IActionContext } from '@microsoft/vscode-azext-utils';
import * as vscode from 'vscode';
import { TelemetryId } from '../telemetry/telemetryId';
import { createAzureDevCli } from '../utils/azureDevCli';
import { executeAsTask } from '../utils/executeAsTask';
import { getAzDevTerminalTitle, getWorkingFolder } from './cmdUtil';

export async function deploy(context: IActionContext, selectedFile?: vscode.Uri): Promise<void> {
    const workingFolder = await getWorkingFolder(context, selectedFile);

    const azureCli = createAzureDevCli();

    // Only supporting "deploy all" mode (no support for deploying individual services)
    // until https://github.com/Azure/azure-dev/issues/696 is resolved.
    const command = azureCli.commandBuilder.withArg('deploy').build();

    void executeAsTask(command, getAzDevTerminalTitle(), {
        alwaysRunNew: true,
        cwd: workingFolder,
        env: azureCli.env
    }, TelemetryId.DeployCli);
}
