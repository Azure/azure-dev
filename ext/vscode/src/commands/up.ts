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

export async function up(context: IActionContext, selectedItem?: vscode.Uri | TreeViewModel): Promise<void> {
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
