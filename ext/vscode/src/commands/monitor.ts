// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

import { IActionContext, IAzureQuickPickItem, parseError, UserCancelledError } from '@microsoft/vscode-azext-utils';
import { composeArgs, withArg } from '@microsoft/vscode-processutils';
import * as vscode from 'vscode';
import { createAzureDevCli } from '../utils/azureDevCli';
import { execAsync } from '../utils/execAsync';
import { isAzureDevCliModel, isTreeViewModel, TreeViewModel } from '../utils/isTreeViewModel';
import { AzureDevCliApplication } from '../views/workspace/AzureDevCliApplication';
import { getWorkingFolder, validateFileSystemUri } from './cmdUtil';

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
    let selectedFile: vscode.Uri | undefined;
    if (isTreeViewModel(selectedItem)) {
        selectedFile = selectedItem.unwrap<AzureDevCliApplication>().context.configurationFile;
    } else if (isAzureDevCliModel(selectedItem)) {
        selectedFile = selectedItem.context.configurationFile;
    } else {
        selectedFile = selectedItem as vscode.Uri;
    }

    // Validate that selectedFile is valid for file system operations
    validateFileSystemUri(context, selectedFile, selectedItem, 'monitor');

    const workingFolder = await getWorkingFolder(context, selectedFile);

    const monitorChoices = await context.ui.showQuickPick(MonitorChoices, {
        canPickMany: true,
        placeHolder: vscode.l10n.t('What monitoring page(s) do you want to open?'),
        isPickSelected: choice => !!choice.picked
    });
    if (!monitorChoices || monitorChoices.length === 0) {
        throw new UserCancelledError();
    }

    const azureCli = await createAzureDevCli(context);
    const args = composeArgs(
        withArg('monitor'),
        withArg(...monitorChoices.map(c => c.data)),
    )();

    const progressOptions: vscode.ProgressOptions = {
        location: vscode.ProgressLocation.Notification,
        title: vscode.l10n.t('Opening monitoring page(s)...'),
    };
    try {
        await vscode.window.withProgress(progressOptions, async () => {
            await execAsync(azureCli.invocation, args, azureCli.spawnOptions(workingFolder));
        });
    } catch (err) {
        const parsedErr = parseError(err);
        if (!parsedErr.isUserCancelledError) {
            await vscode.window.showErrorMessage(
                vscode.l10n.t("Command '{0}' returned an error", 'monitor'),
                { modal: true, detail: parsedErr.message }
            );
        }
    }
}
