// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

import * as vscode from 'vscode';
import { IActionContext } from "@microsoft/vscode-azext-utils";
import { getAzDevTerminalTitle, getWorkingFolder, } from './cmdUtil';
import { createAzureDevCli } from '../utils/azureDevCli';
import { executeAsTask } from '../utils/executeAsTask';
import { TelemetryId } from '../telemetry/telemetryId';
import { AzureDevCliApplication } from '../views/workspace/AzureDevCliApplication';
import { isTreeViewModel, TreeViewModel } from '../utils/isTreeViewModel';
import { localize } from '../localize';
import { MessageItem } from 'vscode';
import { DialogResponses } from '@microsoft/vscode-azext-utils';

export async function down(context: IActionContext, selectedItem?: vscode.Uri | TreeViewModel): Promise<void> {
    const selectedFile = isTreeViewModel(selectedItem) ? selectedItem.unwrap<AzureDevCliApplication>().context.configurationFile : selectedItem;
    const workingFolder = await getWorkingFolder(context, selectedFile);

    const confirmPrompt = localize('azure-dev.commands.cli.down.confirm-prompt', "Are you sure you want to delete all application's Azure resources? You can soft-delete certain resources like Azure KeyVaults to preserve their data, or permanently delete and purge them.");
    const confirmTitle = localize('azure-dev.commands.cli.down.confirm-ack', "Delete resources");

    const softDelete: MessageItem = { title: localize("azure-dev.commands.cli.down.soft-delete", "Soft Delete") };
    const purgeDelete: MessageItem = { title: localize("azure-dev.commands.cli.down.purge-delete", "Delete and Purge") };

    // If cancel is chosen or the modal is closed, a `UserCancelledError` will automatically be thrown, so we don't need to check for it
    const choice = await context.ui.showWarningMessage(confirmPrompt, { modal: true }, { title: confirmTitle }, purgeDelete, softDelete, DialogResponses.cancel);

    context.telemetry.properties.purge = choice === purgeDelete ? 'true' : 'false';

    const azureCli = await createAzureDevCli(context);
    const command = azureCli.commandBuilder.withArg('down').withArg('--force');
    if (choice === purgeDelete) {
        command.withArg('--purge');
    }

    // Don't wait
    void executeAsTask(command.build(), getAzDevTerminalTitle(), {
        alwaysRunNew: true,
        cwd: workingFolder,
        env: azureCli.env
    }, TelemetryId.DownCli);
}
