// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

import { IActionContext } from '@microsoft/vscode-azext-utils';
import { composeArgs, withArg } from '@microsoft/vscode-processutils';
import * as vscode from 'vscode';
import { TelemetryId } from '../telemetry/telemetryId';
import { createAzureDevCli } from '../utils/azureDevCli';
import { executeAsTask } from '../utils/executeAsTask';
import { isAzureDevCliModel, isTreeViewModel, TreeViewModel } from '../utils/isTreeViewModel';
import { AzureDevCliModel } from '../views/workspace/AzureDevCliModel';
import { AzureDevCliService } from '../views/workspace/AzureDevCliService';
import { getAzDevTerminalTitle, getWorkingFolder } from './cmdUtil';

export async function restore(context: IActionContext, selectedItem?: vscode.Uri | TreeViewModel): Promise<void> {
    let selectedModel: AzureDevCliModel | undefined;
    let selectedFile: vscode.Uri | undefined;

    if (isTreeViewModel(selectedItem)) {
        selectedModel = selectedItem.unwrap<AzureDevCliModel>();
        selectedFile = selectedModel.context.configurationFile;
    } else if (isAzureDevCliModel(selectedItem)) {
        selectedModel = selectedItem;
        selectedFile = selectedModel.context.configurationFile;
    } else {
        selectedFile = selectedItem as vscode.Uri;
    }
    const workingFolder = await getWorkingFolder(context, selectedFile);

    const azureCli = await createAzureDevCli(context);
    const args = composeArgs(
        withArg('restore'),
        withArg(selectedModel instanceof AzureDevCliService ? selectedModel.name : '--all'),
    )();

    void executeAsTask(azureCli.invocation, args, getAzDevTerminalTitle(), azureCli.spawnOptions(workingFolder), {
        alwaysRunNew: true,
    }, TelemetryId.RestoreCli);
}
