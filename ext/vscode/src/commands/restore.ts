// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

import { IActionContext } from '@microsoft/vscode-azext-utils';
import * as vscode from 'vscode';
import { TelemetryId } from '../telemetry/telemetryId';
import { createAzureDevCli } from '../utils/azureDevCli';
import { executeAsTask } from '../utils/executeAsTask';
import { getAzDevTerminalTitle, getWorkingFolder } from './cmdUtil';

export async function restore(context: IActionContext, selectedFile?: vscode.Uri): Promise<void> {
    const workingFolder = await getWorkingFolder(context, selectedFile);

    const azureCli = await createAzureDevCli(context);

    const command = azureCli.commandBuilder.withArg('restore').build();

    void executeAsTask(command, getAzDevTerminalTitle(), {
        alwaysRunNew: true,
        cwd: workingFolder,
        env: azureCli.env
    }, TelemetryId.RestoreCli);
}
