// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

import * as vscode from 'vscode';
import { IActionContext } from "@microsoft/vscode-azext-utils";
import { getAzDevTerminalTitle, getWorkingFolder } from './cmdUtil';
import { createAzureDevCli } from '../utils/azureDevCli';
import { executeAsTask } from '../utils/executeAsTask';
import { TelemetryId } from '../telemetry/telemetryId';
import { AzureDevCliApplication } from '../views/workspace/AzureDevCliApplication';
import { isTreeViewModel, TreeViewModel } from '../utils/isTreeViewModel';

/**
 * A tuple representing the arguments that must be passed to the `up` command when executed via {@link vscode.commands.executeCommand}
 */
export type UpCommandArguments = [ vscode.Uri | TreeViewModel | undefined, boolean? ];

export async function up(context: IActionContext, selectedItem?: vscode.Uri | TreeViewModel, fromAgent: boolean = false): Promise<void> {
    context.telemetry.properties.fromAgent = fromAgent.toString();

    const selectedFile = isTreeViewModel(selectedItem) ? selectedItem.unwrap<AzureDevCliApplication>().context.configurationFile : selectedItem;
    const workingFolder = await getWorkingFolder(context, selectedFile);

    const azureCli = await createAzureDevCli(context);
    const command = azureCli.commandBuilder
        .withArg('up');

    // Don't wait
    void executeAsTask(command.build(), getAzDevTerminalTitle(), {
        focus: true,
        alwaysRunNew: true,
        cwd: workingFolder,
        env: azureCli.env
    }, TelemetryId.UpCli);
}
