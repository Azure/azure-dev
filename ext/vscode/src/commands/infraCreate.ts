// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

import { IActionContext } from '@microsoft/vscode-azext-utils';
import * as vscode from 'vscode';
import { TelemetryId } from '../telemetry/telemetryId';
import { createAzureDevCli } from '../utils/azureDevCli';
import { executeAsTask } from '../utils/executeAsTask';
import { isTreeViewModel, TreeViewModel } from '../utils/isTreeViewModel';
import { AzureDevCliApplication } from '../views/workspace/AzureDevCliApplication';
import { getAzDevTerminalTitle, getWorkingFolder } from './cmdUtil';

export async function infraCreate(context: IActionContext, selectedItem?: vscode.Uri | TreeViewModel): Promise<void> {
    const selectedFile = isTreeViewModel(selectedItem) ? selectedItem.unwrap<AzureDevCliApplication>().configurationFile : selectedItem;

    const workingFolder = await getWorkingFolder(context, selectedFile);

    const azureCli = await createAzureDevCli(context);

    const command = azureCli.commandBuilder.withArg('infra').withArg('create').build();

    void executeAsTask(command, getAzDevTerminalTitle(), {
        alwaysRunNew: true,
        cwd: workingFolder,
        env: azureCli.env
    }, TelemetryId.DeployCli);
}
