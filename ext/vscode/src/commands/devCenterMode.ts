// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

import { IActionContext } from '@microsoft/vscode-azext-utils';
import { composeArgs, withArg, withNamedArg } from '@microsoft/vscode-processutils';
import * as vscode from 'vscode';
import { createAzureDevCli } from '../utils/azureDevCli';
import { execAsync } from '../utils/execAsync';

export async function enableDevCenterMode(context: IActionContext): Promise<void> {
    const azureCli = await createAzureDevCli(context);
    const args = composeArgs(
        withArg('config', 'set'),
        withNamedArg('platform.type', 'devcenter'),
    )();

    await execAsync(azureCli.invocation, args, azureCli.spawnOptions());
    void vscode.window.showInformationMessage(vscode.l10n.t('Azure Developer CLI\'s Dev Center mode has been enabled.'));
}

export async function disableDevCenterMode(context: IActionContext): Promise<void> {
    const azureCli = await createAzureDevCli(context);
    const args = composeArgs(
        withArg('config', 'unset', 'platform.type'),
    )();

    await execAsync(azureCli.invocation, args, azureCli.spawnOptions());
    void vscode.window.showInformationMessage(vscode.l10n.t('Azure Developer CLI\'s Dev Center mode has been disabled.'));
}
