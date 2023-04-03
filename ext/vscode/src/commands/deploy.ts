// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

import { IActionContext } from '@microsoft/vscode-azext-utils';
import * as vscode from 'vscode';
import { TelemetryId } from '../telemetry/telemetryId';
import { createAzureDevCli } from '../utils/azureDevCli';
import { executeAsTask } from '../utils/executeAsTask';
import { isTreeViewModel, TreeViewModel } from '../utils/isTreeViewModel';
import { AzureDevCliModel } from '../views/workspace/AzureDevCliModel';
import { AzureDevCliService } from '../views/workspace/AzureDevCliService';
import { getAzDevTerminalTitle, getWorkingFolder } from './cmdUtil';

export async function deploy(context: IActionContext, selectedItem?: vscode.Uri | TreeViewModel): Promise<void> {
    const selectedModel = isTreeViewModel(selectedItem) ? selectedItem.unwrap<AzureDevCliModel>() : undefined;
    const selectedFile = isTreeViewModel(selectedItem) ? selectedItem.unwrap<AzureDevCliModel>().context.configurationFile : selectedItem;
    const workingFolder = await getWorkingFolder(context, selectedFile);

    const azureCli = await createAzureDevCli(context);

    const commandBuilder = azureCli.commandBuilder.withArg('deploy');

    if (selectedModel instanceof AzureDevCliService) {
        commandBuilder.withArg(selectedModel.name);
    } else {
        commandBuilder.withArg('--all');
    }
    
    const command = commandBuilder.build();

    void executeAsTask(command, getAzDevTerminalTitle(), {
        alwaysRunNew: true,
        cwd: workingFolder,
        env: azureCli.env
    }, TelemetryId.DeployCli);
}
