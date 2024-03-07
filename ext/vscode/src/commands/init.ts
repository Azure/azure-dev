// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

import * as vscode from 'vscode';
import { IActionContext } from '@microsoft/vscode-azext-utils';
import { createAzureDevCli } from '../utils/azureDevCli';
import { quickPickWorkspaceFolder } from '../utils/quickPickWorkspaceFolder';
import { executeAsTask } from '../utils/executeAsTask';
import { getAzDevTerminalTitle, selectApplicationTemplate, showReadmeFile } from './cmdUtil';
import { TelemetryId } from '../telemetry/telemetryId';

interface InitCommandOptions {
    templateUrl?: string;
    environmentName?: string;
}

export async function init(context: IActionContext, selectedFile?: vscode.Uri, allSelectedFiles?: vscode.Uri, options?: InitCommandOptions): Promise<void> {
    let folder: vscode.WorkspaceFolder | undefined = (selectedFile ? vscode.workspace.getWorkspaceFolder(selectedFile) : undefined);
    if (!folder) {
        folder = await quickPickWorkspaceFolder(context, vscode.l10n.t("To run '{0}' command you must first open a folder or workspace in VS Code", 'init'));
    }

    const templateUrl = options?.templateUrl ?? await selectApplicationTemplate(context);

    const azureCli = await createAzureDevCli(context);
    const command = azureCli.commandBuilder
        .withArg('init')
        .withNamedArg('-t', {value: templateUrl, quoting: vscode.ShellQuoting.Strong});
    const workspacePath = folder?.uri;

    if (options?.environmentName) {
        command.withNamedArg('-e', {value: options.environmentName, quoting: vscode.ShellQuoting.Strong});
    }

    // Don't wait
    void executeAsTask(command.build(), getAzDevTerminalTitle(), {
        alwaysRunNew: true,
        cwd: workspacePath.fsPath,
        env: azureCli.env
    }, TelemetryId.InitCli).then(() => {
        void showReadmeFile(workspacePath);
    });
}
