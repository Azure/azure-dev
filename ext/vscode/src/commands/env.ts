// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

import * as vscode from 'vscode';
import { IActionContext, IAzureQuickPickItem, parseError } from '@microsoft/vscode-azext-utils';
import { localize } from '../localize';
import { createAzureDevCli } from '../utils/azureDevCli';
import { quickPickWorkspaceFolder } from '../utils/quickPickWorkspaceFolder';
import { EnvironmentInfo, getAzDevTerminalTitle, getEnvironments } from './cmdUtil';
import { executeInTerminal } from '../utils/executeInTerminal';
import { spawnAsync } from '../utils/process';

export async function selectEnvironment(context: IActionContext, selectedFile?: vscode.Uri): Promise<void> {
    let folder: vscode.WorkspaceFolder | undefined = (selectedFile ? vscode.workspace.getWorkspaceFolder(selectedFile) : undefined);
    if (!folder) {
        folder = await quickPickWorkspaceFolder(context, localize('azure-dev.commands.util.needWorkspaceFolder', "To run '{0}' command you must first open a folder or workspace in VS Code", 'env select'));
    }
    const cwd = folder.uri.fsPath;

    let envData: EnvironmentInfo[] = [];
    let errorMsg: string | undefined = undefined;
    try {
        envData = await getEnvironments(context, cwd);
    } catch(err) {
        errorMsg = parseError(err).message;
        // Treated the same as no environments case
    }

    if (envData.length === 0) {
        await promptCreateNewEnvironment(localize('azure-dev.commands.cli.env-select.no-environments', 'There are no environments to select. Would you like to create one?'), errorMsg);
        return; // promptCreateNewEnvironment() will call newEnvironment() asynchronously if necessary
    }

    const envChoices  = envData.map(d => ({ label: d.Name, data: d,} as IAzureQuickPickItem<EnvironmentInfo>));
    const selectedEnv = await context.ui.showQuickPick(envChoices, {
        canPickMany: false,
        title: localize('azure-dev.commands.cli.env-select.choose-environment', 'What environment should be set as default?')
    });

    const azureCli = await createAzureDevCli(context);
    azureCli.commandBuilder.withArg('env').withArg('select').withQuotedArg(selectedEnv.data.Name);
    await spawnAsync(azureCli.commandBuilder.build(), azureCli.spawnOptions(cwd));
    await vscode.window.showInformationMessage(
        localize('azure-dev.commands.cli.env-select.environment-selected', "'{0}' is now the default environment", selectedEnv.data.Name));
}

export async function newEnvironment(context: IActionContext, selectedFile?: vscode.Uri): Promise<void> {
    let folder: vscode.WorkspaceFolder | undefined = (selectedFile ? vscode.workspace.getWorkspaceFolder(selectedFile) : undefined);
    if (!folder) {
        folder = await quickPickWorkspaceFolder(context, localize('azure-dev.commands.util.needWorkspaceFolder', "To run '{0}' command you must first open a folder or workspace in VS Code", 'env new'));
    }

    const azureCli = await createAzureDevCli(context);
    const command = azureCli.commandBuilder.withArg('env').withArg('new');
    const options: vscode.TerminalOptions = {
        name: getAzDevTerminalTitle(),
        cwd: folder.uri,
        env: azureCli.env
    };

    void executeInTerminal(command.build(), options);
}

export async function refreshEnvironment(context: IActionContext, selectedFile?: vscode.Uri): Promise<void> {
    let folder: vscode.WorkspaceFolder | undefined = (selectedFile ? vscode.workspace.getWorkspaceFolder(selectedFile) : undefined);
    if (!folder) {
        folder = await quickPickWorkspaceFolder(context, localize('azure-dev.commands.util.needWorkspaceFolder', "To run '{0}' command you must first open a folder or workspace in VS Code", 'env refresh'));
    }
    const cwd = folder.uri.fsPath;

    const azureCli = await createAzureDevCli(context);
    const progressOptions: vscode.ProgressOptions = {
        location: vscode.ProgressLocation.Notification,
        title: localize('azure-dev.commands.cli.env-refresh.refreshing', 'Refreshing environment values...'),
    };
    azureCli.commandBuilder.withArg('env').withArg('refresh');
    let errorMsg: string | undefined = undefined;

    await vscode.window.withProgress(progressOptions, async () => {
        try {
            await spawnAsync(azureCli.commandBuilder.build(), azureCli.spawnOptions(cwd));
        } catch(err) {
            errorMsg = parseError(err).message;
        }
    });
    if (errorMsg) {
        await promptCreateNewEnvironment(
            localize('azure-dev.commands.cli.env-refresh.failure', 'Environment values could not be refreshed. Infrastructure might have never been provisioned in Azure, or there might be no environments. Would you like to create one?'), errorMsg);
    }
}

async function promptCreateNewEnvironment(message: string, details?: string): Promise<void> {
    const createNewEnvItem: vscode.MessageItem = { 
        title: localize('azure-dev.commands.cli.env-new.create-new-env', 'Create a new environment'),
        isCloseAffordance: false
    };
    const cancelItem: vscode.MessageItem = { 
        title: localize('azure-dev.commands.cli.env-new.cancel', 'Cancel'),
        isCloseAffordance: true
    };
    const selectedItem = await vscode.window.showErrorMessage(message, 
        { modal: true, detail: details }, createNewEnvItem, cancelItem);
    if (selectedItem === createNewEnvItem) {
        void vscode.commands.executeCommand('azure-dev.commands.cli.env-new'); // Don't wait
    }
}
