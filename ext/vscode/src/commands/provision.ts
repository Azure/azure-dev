// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

import * as vscode from 'vscode';
import { IActionContext } from '@microsoft/vscode-azext-utils';
import { executeAsTask } from '../utils/executeAsTask';
import { getAzDevTerminalTitle, getWorkingFolder } from './cmdUtil';
import { createAzureDevCli } from '../utils/azureDevCli';
import { TelemetryId } from '../telemetry/telemetryId';
import { isTreeViewModel, TreeViewModel } from '../utils/isTreeViewModel';
import { AzureDevCliApplication } from '../views/workspace/AzureDevCliApplication';

export async function provision(context: IActionContext, selectedItem?: vscode.Uri | TreeViewModel): Promise<void> {
    const selectedFile = isTreeViewModel(selectedItem) ? selectedItem.unwrap<AzureDevCliApplication>().context.configurationFile : selectedItem;
    const workingFolder = await getWorkingFolder(context, selectedFile);

    const azureCli = await createAzureDevCli(context);
    const command = azureCli.commandBuilder.withArg('provision').build();

    // Don't wait
    void executeAsTask(command, getAzDevTerminalTitle(), {
        alwaysRunNew: true, 
        cwd: workingFolder,
        env: azureCli.env
    }, TelemetryId.ProvisionCli);
}
