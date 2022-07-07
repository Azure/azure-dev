// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

import * as vscode from 'vscode';
import { IActionContext, IAzureQuickPickItem, parseError, UserCancelledError } from '@microsoft/vscode-azext-utils';
import { localize } from '../localize';
import { createAzureDevCli } from '../utils/azureDevCli';
import { quickPickWorkspaceFolder } from '../utils/quickPickWorkspaceFolder';
import { spawnAsync } from '../utils/process';

const MonitorChoices: IAzureQuickPickItem<string>[] = [
    {
        label: localize('azure-dev.commands.cli.monitor.open-live-metrics', 'Application Insights Live Metrics'),
        data: '--live', suppressPersistence: true
    },
    {
        label: localize('azure-dev.commands.cli.monitor.open-logs', 'Application Insights Logs'),
        data: '--logs', suppressPersistence: true
    },
    {
        label: localize('azure-dev.commands.cli.monitor.open-overview', 'Application Insights Overview Dashboard'),
        data: '--overview', suppressPersistence: true,
        picked: true
    }
];

export async function monitor(context: IActionContext, selectedFile?: vscode.Uri): Promise<void> {
    let folder: vscode.WorkspaceFolder | undefined = (selectedFile ? vscode.workspace.getWorkspaceFolder(selectedFile) : undefined);
    if (!folder) {
        folder = await quickPickWorkspaceFolder(context, localize('azure-dev.commands.util.needWorkspaceFolder', "To run '{0}' command you must first open a folder or workspace in VS Code", 'monitor'));
    }
    const cwd = folder.uri.fsPath;

    const monitorChoices  = await context.ui.showQuickPick(MonitorChoices, {
        canPickMany: true,
        placeHolder: localize('azure-dev.commands.cli.monitor.choose-pages', 'What monitoring page(s) do you want to open?'),
        isPickSelected: choice => !!choice.picked 
    });
    if (!monitorChoices || monitorChoices.length === 0) {
        throw new UserCancelledError();
    }

    const azureCli = createAzureDevCli();
    let command = azureCli.commandBuilder.withArg('monitor');
    for (const choice of monitorChoices) {
        command = command.withArg(choice.data);
    }

    const progressOptions: vscode.ProgressOptions = {
        location: vscode.ProgressLocation.Notification,
        title: localize('azure-dev.commands.cli.monitor.opening-pages', 'Opening monitoring page(s)...'),
    };
    try {
        await vscode.window.withProgress(progressOptions, async () => {
            await spawnAsync(command.build(), azureCli.spawnOptions(cwd));
        });
    }
    catch(err) {
        const parsedErr = parseError(err);
        if (!parsedErr.isUserCancelledError) {
            await vscode.window.showErrorMessage(
                localize("azure-dev.commands.util.invocation-error", "Command '{0}' returned an error", 'monitor'),
                { modal: true, detail: parsedErr.message }
            );
        }
    }
}
