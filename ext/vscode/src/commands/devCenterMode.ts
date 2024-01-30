// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

import { IActionContext } from '@microsoft/vscode-azext-utils';
import * as vscode from 'vscode';
import { createAzureDevCli } from '../utils/azureDevCli';
import { execAsync } from '../utils/process';

export async function enableDevCenterMode(context: IActionContext): Promise<void> {
    const azureCli = await createAzureDevCli(context);
    const command = azureCli.commandBuilder
        .withArg('config')
        .withArg('set')
        .withArg('platform.type')
        .withArg('devcenter');

    await execAsync(command.build(), azureCli.spawnOptions());
    void vscode.window.showInformationMessage(vscode.l10n.t('Azure Developer CLI\'s Dev Center mode has been enabled.'));
}

export async function disableDevCenterMode(context: IActionContext): Promise<void> {
    const azureCli = await createAzureDevCli(context);
    const command = azureCli.commandBuilder
        .withArg('config')
        .withArg('unset')
        .withArg('platform.type');

    await execAsync(command.build(), azureCli.spawnOptions());
    void vscode.window.showInformationMessage(vscode.l10n.t('Azure Developer CLI\'s Dev Center mode has been disabled.'));
}
