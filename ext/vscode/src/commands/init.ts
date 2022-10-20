// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

import * as vscode from 'vscode';
import { IActionContext } from '@microsoft/vscode-azext-utils';
import { localize } from '../localize';
import { createAzureDevCli } from '../utils/azureDevCli';
import { quickPickWorkspaceFolder } from '../utils/quickPickWorkspaceFolder';
import { executeAsTask } from '../utils/executeAsTask';
import { getAzDevTerminalTitle, selectApplicationTemplate, showReadmeFile } from './cmdUtil';
import { TelemetryId } from '../telemetry/telemetryId';

export async function init(context: IActionContext, selectedFile?: vscode.Uri, allSelectedFiles?: vscode.Uri): Promise<void> {
    let folder: vscode.WorkspaceFolder | undefined = (selectedFile ? vscode.workspace.getWorkspaceFolder(selectedFile) : undefined);
    if (!folder) {
        folder = await quickPickWorkspaceFolder(context, localize('azure-dev.commands.util.needWorkspaceFolder', "To run '{0}' command you must first open a folder or workspace in VS Code", 'init'));
    }

    const templateUrl = await selectApplicationTemplate(context);

    const azureCli = await createAzureDevCli(context);
    const command = azureCli.commandBuilder
        .withArg('init')
        .withNamedArg('-t', {value: templateUrl, quoting: vscode.ShellQuoting.Strong});
    const workspacePath = folder?.uri;

    // Don't wait
    void executeAsTask(command.build(), getAzDevTerminalTitle(), {
        alwaysRunNew: true,
        cwd: workspacePath.fsPath,
        env: azureCli.env
    }, TelemetryId.InitCli).then(() => {
        void showReadmeFile(workspacePath);
    });
}
