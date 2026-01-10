// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

import { IActionContext } from "@microsoft/vscode-azext-utils";
import { composeArgs, withArg } from '@microsoft/vscode-processutils';
import * as vscode from 'vscode';
import { TelemetryId } from '../telemetry/telemetryId';
import { createAzureDevCli } from '../utils/azureDevCli';
import { executeAsTask } from '../utils/executeAsTask';
import { isTreeViewModel, TreeViewModel } from '../utils/isTreeViewModel';
import { AzureDevCliApplication } from '../views/workspace/AzureDevCliApplication';
import { getAzDevTerminalTitle, getWorkingFolder } from './cmdUtil';

/**
 * A tuple representing the arguments that must be passed to the `up` command when executed via {@link vscode.commands.executeCommand}
 */
export type UpCommandArguments = [ vscode.Uri | TreeViewModel | undefined, boolean? ];

export async function up(context: IActionContext, selectedItem?: vscode.Uri | TreeViewModel, fromAgent: boolean = false): Promise<void> {
    context.telemetry.properties.fromAgent = fromAgent.toString();

    const selectedFile = isTreeViewModel(selectedItem) ? selectedItem.unwrap<AzureDevCliApplication>().context.configurationFile : selectedItem;
    const workingFolder = await getWorkingFolder(context, selectedFile);

    const azureCli = await createAzureDevCli(context);
    const args = composeArgs(
        withArg('up'),
    )();

    // Don't wait
    void executeAsTask(azureCli.invocation, args, getAzDevTerminalTitle(), azureCli.spawnOptions(workingFolder), {
        focus: true,
        alwaysRunNew: true,
    }, TelemetryId.UpCli);
}
