// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License. 

import * as vscode from 'vscode';
import { IActionContext } from '@microsoft/vscode-azext-utils';
import { getAzDevTerminalTitle, getWorkingFolder } from './cmdUtil';
import { executeAsTask } from '../utils/executeAsTask';
import { createAzureDevCli } from '../utils/azureDevCli';
import { TelemetryId } from '../telemetry/telemetryId';

export async function infraDelete(context: IActionContext, selectedFile?: vscode.Uri): Promise<void> {
    const workingFolder = await getWorkingFolder(context, selectedFile);
    const confirmPrompt = vscode.l10n.t("Are you sure to delete all application's Azure resources?");
    const confirmAck = vscode.l10n.t("Delete resources");

    await context.ui.showWarningMessage(confirmPrompt, { modal: true }, { title: confirmAck });

    const choices = [
        {
            label: vscode.l10n.t("Do not purge"),
            data: false
        },
        {
            label: vscode.l10n.t("Permanently delete (purge)"),
            data: true
        }
    ];
    const purgeChoice = await context.ui.showQuickPick(choices, {
        placeHolder: vscode.l10n.t("Permanently delete resources with are soft-deleted by default (e.g. KeyVaults)?"), // Should this be title?
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
