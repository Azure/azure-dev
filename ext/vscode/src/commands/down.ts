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

export async function down(context: IActionContext, selectedItem?: vscode.Uri | TreeViewModel): Promise<void> {
    const selectedFile = isTreeViewModel(selectedItem) ? selectedItem.unwrap<AzureDevCliApplication>().context.configurationFile : selectedItem;
    const workingFolder = await getWorkingFolder(context, selectedFile);

    const confirmPrompt = localize('azure-dev.commands.cli.down.confirm-prompt', "Are you sure you want to delete all application's Azure resources?");
    const confirmAck = localize('azure-dev.commands.cli.down.confirm-ack', "Delete resources");

    await context.ui.showWarningMessage(confirmPrompt, { modal: true }, { title: confirmAck });

    const choices = [
        {
            label: localize("azure-dev.commands.cli.down.no-purge", "Do not purge"),
            data: false
        },
        {
            label: localize("azure-dev.commands.cli.down.purge", "Permanently delete (purge)"),
            data: true
        }
    ];
    const purgeChoice = await context.ui.showQuickPick(choices, {
        placeHolder: localize("azure-dev.commands.cli.down.purge-prompt", "Permanently delete resources with are soft-deleted by default (e.g. KeyVaults)?"), // TODO: Should this be title?
        suppressPersistence: true,
        canPickMany: false,
    });
    context.telemetry.properties.purge = purgeChoice ? 'true' : 'false';

    const azureCli = await createAzureDevCli(context);
    const command = azureCli.commandBuilder.withArg('down').withArg('--force');
    if (purgeChoice.data) {
        command.withArg('--purge');
    }

    // Don't wait
    void executeAsTask(command.build(), getAzDevTerminalTitle(), {
        alwaysRunNew: true,
        cwd: workingFolder,
        env: azureCli.env
    }, TelemetryId.DownCli);
}
