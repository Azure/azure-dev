// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

import * as vscode from 'vscode';
import { IActionContext, IAzureQuickPickItem, parseError, UserCancelledError } from '@microsoft/vscode-azext-utils';
import { createAzureDevCli } from '../utils/azureDevCli';
import { spawnAsync } from '../utils/process';
import { isTreeViewModel, TreeViewModel } from '../utils/isTreeViewModel';
import { AzureDevCliApplication } from '../views/workspace/AzureDevCliApplication';
import { getWorkingFolder } from './cmdUtil';

const MonitorChoices: IAzureQuickPickItem<string>[] = [
    {
        label: vscode.l10n.t('Application Insights Live Metrics'),
        data: '--live', suppressPersistence: true
    },
    {
        label: vscode.l10n.t('Application Insights Logs'),
        data: '--logs', suppressPersistence: true
    },
    {
        label: vscode.l10n.t('Application Insights Overview Dashboard'),
        data: '--overview', suppressPersistence: true,
        picked: true
    }
];

export async function monitor(context: IActionContext, selectedItem?: vscode.Uri | TreeViewModel): Promise<void> {
    const selectedFile = isTreeViewModel(selectedItem) ? selectedItem.unwrap<AzureDevCliApplication>().context.configurationFile : selectedItem;
    const workingFolder = await getWorkingFolder(context, selectedFile);

    const monitorChoices  = await context.ui.showQuickPick(MonitorChoices, {
        canPickMany: true,
        placeHolder: vscode.l10n.t('What monitoring page(s) do you want to open?'),
        isPickSelected: choice => !!choice.picked 
    });
    if (!monitorChoices || monitorChoices.length === 0) {
        throw new UserCancelledError();
    }

    const azureCli = await createAzureDevCli(context);
    let command = azureCli.commandBuilder.withArg('monitor');
    for (const choice of monitorChoices) {
        command = command.withArg(choice.data);
    }

    const progressOptions: vscode.ProgressOptions = {
        location: vscode.ProgressLocation.Notification,
        title: vscode.l10n.t('Opening monitoring page(s)...'),
    };
    try {
        await vscode.window.withProgress(progressOptions, async () => {
            await spawnAsync(command.build(), azureCli.spawnOptions(workingFolder));
        });
    } catch(err) {
        const parsedErr = parseError(err);
        if (!parsedErr.isUserCancelledError) {
            await vscode.window.showErrorMessage(
                vscode.l10n.t("Command '{0}' returned an error", 'monitor'),
                { modal: true, detail: parsedErr.message }
            );
        }
    }
}
