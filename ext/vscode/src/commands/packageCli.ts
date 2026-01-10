// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

import { IActionContext } from '@microsoft/vscode-azext-utils';
import { composeArgs, withArg } from '@microsoft/vscode-processutils';
import * as vscode from 'vscode';
import { TelemetryId } from '../telemetry/telemetryId';
import { createAzureDevCli } from '../utils/azureDevCli';
import { executeAsTask } from '../utils/executeAsTask';
import { isTreeViewModel, TreeViewModel } from '../utils/isTreeViewModel';
import { AzureDevCliModel } from '../views/workspace/AzureDevCliModel';
import { AzureDevCliService } from '../views/workspace/AzureDevCliService';
import { getAzDevTerminalTitle, getWorkingFolder } from './cmdUtil';

// `package` is a reserved identifier so `packageCli` had to be used instead
export async function packageCli(context: IActionContext, selectedItem?: vscode.Uri | TreeViewModel): Promise<void> {
    const selectedModel = isTreeViewModel(selectedItem) ? selectedItem.unwrap<AzureDevCliModel>() : undefined;
    const selectedFile = isTreeViewModel(selectedItem) ? selectedItem.unwrap<AzureDevCliModel>().context.configurationFile : selectedItem;
    const workingFolder = await getWorkingFolder(context, selectedFile);

    const azureCli = await createAzureDevCli(context);
    const args = composeArgs(
        withArg('package'),
        withArg(selectedModel instanceof AzureDevCliService ? selectedModel.name : '--all'),
    )();

    void executeAsTask(azureCli.invocation, args, getAzDevTerminalTitle(), azureCli.spawnOptions(workingFolder), {
        alwaysRunNew: true,
    }, TelemetryId.PackageCli);
}
