// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

import { IActionContext } from '@microsoft/vscode-azext-utils';
import * as vscode from 'vscode';
import { createAzureDevCli } from '../utils/azureDevCli';
import { execAsync } from '../utils/process';

export function enableDevCenterMode(context: IActionContext): Promise<void> {
    return setDevCenterMode(context, true);
}

export async function disableDevCenterMode(context: IActionContext): Promise<void> {
    return setDevCenterMode(context, false);
}

async function setDevCenterMode(context: IActionContext, enable: boolean): Promise<void> {
    const azureCli = await createAzureDevCli(context);
    const command = azureCli.commandBuilder
        .withArg('config')
        .withArg('set')
        .withArg('devCenterMode')
        .withArg(enable ? 'true' : 'false');

    await execAsync(command.build(), azureCli.spawnOptions());
    void vscode.window.showInformationMessage(vscode.l10n.t('Azure Dev CLI\'s Dev Center mode has been {0}.', enable ? vscode.l10n.t('enabled') : vscode.l10n.t('disabled')));
}
