// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

import { IActionContext } from '@microsoft/vscode-azext-utils';
import { composeArgs, withArg } from '@microsoft/vscode-processutils';
import * as vscode from 'vscode';
import { TelemetryId } from '../telemetry/telemetryId';
import { createAzureDevCli } from '../utils/azureDevCli';
import { executeAsTask } from '../utils/executeAsTask';
import { isAzureDevCliModel, isTreeViewModel, TreeViewModel } from '../utils/isTreeViewModel';
import { AzureDevCliApplication } from '../views/workspace/AzureDevCliApplication';
import { getAzDevTerminalTitle, getWorkingFolder } from './cmdUtil';

export async function provision(context: IActionContext, selectedItem?: vscode.Uri | TreeViewModel): Promise<void> {
    let selectedFile: vscode.Uri | undefined;
    if (isTreeViewModel(selectedItem)) {
        selectedFile = selectedItem.unwrap<AzureDevCliApplication>().context.configurationFile;
    } else if (isAzureDevCliModel(selectedItem)) {
        selectedFile = selectedItem.context.configurationFile;
    } else {
        selectedFile = selectedItem as vscode.Uri;
    }
    const workingFolder = await getWorkingFolder(context, selectedFile);

    const azureCli = await createAzureDevCli(context);
    const args = composeArgs(
        withArg('provision'),
    )();

    // Don't wait
    void executeAsTask(azureCli.invocation, args, getAzDevTerminalTitle(), azureCli.spawnOptions(workingFolder), {
        alwaysRunNew: true,
    }, TelemetryId.ProvisionCli);
}
