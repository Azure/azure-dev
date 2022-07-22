// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License. 

import * as vscode from 'vscode';
import { IActionContext } from '@microsoft/vscode-azext-utils';
import { getAzDevTerminalTitle, getWorkingFolder } from './cmdUtil';
import { localize } from '../localize';
import { executeAsTask } from '../utils/executeAsTask';
import { createAzureDevCli } from '../utils/azureDevCli';
import { TelemetryId } from '../telemetry/telemetryId';

export async function infraDelete(context: IActionContext, selectedFile?: vscode.Uri): Promise<void> {
    const workingFolder = await getWorkingFolder(context, selectedFile);
    const confirmPrompt = localize('azure-dev.commands.cli.infra-delete.confirm-prompt', "Are you sure to delete all application's Azure resources?");
    const confirmAck = localize('azure-dev.commands.cli.infra-delete.confirm-ack', "Delete resources");

    await context.ui.showWarningMessage(confirmPrompt, { modal: true }, { title: confirmAck });

    const choices = [
        {
            label: localize("azure-dev.commands.cli.infra-delete.no-purge", "Do not purge"),
            data: false
        },
        {
            label: localize("azure-dev.commands.cli.infra-delete.purge", "Permanently delete (purge)"),
            data: true
        }
    ];
    const purgeChoice = await context.ui.showQuickPick(choices, {
        placeHolder: localize("azure-dev.commands.cli.infra-delete.purge-prompt", "Permanently delete resources with are soft-deleted by default (e.g. KeyVaults)?"), // Should this be title?
        suppressPersistence: true,
        canPickMany: false,
    });
    context.telemetry.properties.purge = purgeChoice ? 'true' : 'false';

    const azureCli = await createAzureDevCli(context);
    let command = azureCli.commandBuilder.withArg('infra').withArg('delete').withArg('--force');
    if (purgeChoice.data) {
        command = command.withArg('--purge');
    }

    // Don't wait
    void executeAsTask(command.build(), getAzDevTerminalTitle(), {
        alwaysRunNew: true,
        cwd: workingFolder,
        env: azureCli.env
    }, TelemetryId.InfraDeleteCli);
}
