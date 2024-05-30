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
import { MessageItem } from 'vscode';
import { DialogResponses } from '@microsoft/vscode-azext-utils';

/**
 * A tuple representing the arguments that must be passed to the `down` command when executed via {@link vscode.commands.executeCommand}
 */
export type DownCommandArguments = [ vscode.Uri | TreeViewModel | undefined, boolean? ];

export async function down(context: IActionContext, selectedItem?: vscode.Uri | TreeViewModel, fromAgent: boolean = false): Promise<void> {
    context.telemetry.properties.fromAgent = fromAgent.toString();

    const selectedFile = isTreeViewModel(selectedItem) ? selectedItem.unwrap<AzureDevCliApplication>().context.configurationFile : selectedItem;
    const workingFolder = await getWorkingFolder(context, selectedFile);

    const confirmPrompt = vscode.l10n.t("Are you sure you want to delete all this application's Azure resources? You can soft-delete certain resources like Azure KeyVaults to preserve their data, or permanently delete and purge them.");

    const softDelete: MessageItem = { title: vscode.l10n.t("Soft Delete") };
    const purgeDelete: MessageItem = { title: vscode.l10n.t("Delete and Purge") };

    // If cancel is chosen or the modal is closed, a `UserCancelledError` will automatically be thrown, so we don't need to check for it
    const choice = await context.ui.showWarningMessage(confirmPrompt, { modal: true }, softDelete, purgeDelete, DialogResponses.cancel);

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
