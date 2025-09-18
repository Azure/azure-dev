// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

import { IActionContext, IAzureQuickPickItem, parseError } from '@microsoft/vscode-azext-utils';
import { composeArgs, withArg, withNamedArg, withQuotedArg } from '@microsoft/vscode-processutils';
import * as vscode from 'vscode';
import ext from '../ext';
import { TelemetryId } from '../telemetry/telemetryId';
import { createAzureDevCli } from '../utils/azureDevCli';
import { execAsync } from '../utils/execAsync';
import { executeAsTask } from '../utils/executeAsTask';
import { isTreeViewModel, TreeViewModel } from '../utils/isTreeViewModel';
import { quickPickWorkspaceFolder } from '../utils/quickPickWorkspaceFolder';
import { AzureDevCliEnvironments } from '../views/workspace/AzureDevCliEnvironments';
import { AzureDevCliEnvironment } from '../views/workspace/AzureDevCliEnvironment';
import { EnvironmentInfo, getAzDevTerminalTitle, getEnvironments } from './cmdUtil';

export async function editEnvironment(context: IActionContext, selectedEnvironment?: TreeViewModel): Promise<void> {
    if (selectedEnvironment) {
        const environment = selectedEnvironment.unwrap<AzureDevCliEnvironment>();

        if (environment.environmentFile) {
            const document = await vscode.workspace.openTextDocument(environment.environmentFile);

            await vscode.window.showTextDocument(document);
        }
    }
}

export async function deleteEnvironment(context: IActionContext, selectedItem?: vscode.Uri | TreeViewModel): Promise<void> {
    const selectedEnvironment = isTreeViewModel(selectedItem) ? selectedItem.unwrap<AzureDevCliEnvironment>() : undefined;
    const selectedFile = isTreeViewModel(selectedItem) ? selectedItem.unwrap<AzureDevCliEnvironments>().context.configurationFile : selectedItem;
    let folder: vscode.WorkspaceFolder | undefined = (selectedFile ? vscode.workspace.getWorkspaceFolder(selectedFile) : undefined);
    if (!folder) {
        folder = await quickPickWorkspaceFolder(context, vscode.l10n.t("To run '{0}' command you must first open a folder or workspace in VS Code", 'env select'));
    }
    const cwd = folder.uri.fsPath;

    let name = selectedEnvironment?.name;

    if (!name) {
        let envData: EnvironmentInfo[] = [];
        try {
            envData = await getEnvironments(context, cwd);
        } catch (err) {
            // Treated the same as no environments case
            const errorMsg = parseError(err).message;
            ext.outputChannel.appendLog(vscode.l10n.t('Error while getting environments: {0}', errorMsg));
        }

        // Filter out the default environment, it cannot be deleted without causing trouble
        envData = envData.filter(e => !e.IsDefault);

        if (envData.length === 0) {
            void vscode.window.showInformationMessage(vscode.l10n.t('There are no environments to delete.'));
            return;
        }

        const envChoices  = envData.map(d => ({ label: d.Name, data: d,} as IAzureQuickPickItem<EnvironmentInfo>));
        const selectedEnv = await context.ui.showQuickPick(envChoices, {
            canPickMany: false,
            title: vscode.l10n.t('Which environment should be deleted?')
        });

        name = selectedEnv.data.Name;
    }

    const deleteOption: vscode.MessageItem = { title: vscode.l10n.t('Delete') };

    const result = await vscode.window.showWarningMessage(
        vscode.l10n.t('Are you sure you want to delete the {0} environment?', name),
        { modal: true },
        deleteOption);

    if (result === deleteOption) {
        const environmentDirectory = vscode.Uri.joinPath(folder.uri, '.azure', name);

        await vscode.workspace.fs.delete(environmentDirectory, { recursive: true, useTrash: false });

        // TODO: Use Azure Developer CLI to delete environment. https://github.com/Azure/azure-dev/issues/1554
        // const azureCli = await createAzureDevCli(context);
        // azureCli.commandBuilder.withArg('env').withArg('delete').withQuotedArg(name);
        // await spawnAsync(azureCli.commandBuilder.build(), azureCli.spawnOptions(cwd));

        void vscode.window.showInformationMessage(
            vscode.l10n.t("'{0}' has been deleted.", name));

        if (selectedEnvironment) {
            selectedEnvironment?.context.refreshEnvironments();
        }
    }
}

export async function selectEnvironment(context: IActionContext, selectedItem?: vscode.Uri | TreeViewModel): Promise<void> {
    const selectedEnvironment = isTreeViewModel(selectedItem) ? selectedItem.unwrap<AzureDevCliEnvironment>() : undefined;
    const selectedFile = isTreeViewModel(selectedItem) ? selectedItem.unwrap<AzureDevCliEnvironments>().context.configurationFile : selectedItem;
    let folder: vscode.WorkspaceFolder | undefined = (selectedFile ? vscode.workspace.getWorkspaceFolder(selectedFile) : undefined);
    if (!folder) {
        folder = await quickPickWorkspaceFolder(context, vscode.l10n.t("To run '{0}' command you must first open a folder or workspace in VS Code", 'env select'));
    }
    const cwd = folder.uri.fsPath;

    let name = selectedEnvironment?.name;

    if (!name) {
        let envData: EnvironmentInfo[] = [];
        let errorMsg: string | undefined = undefined;
        try {
            envData = await getEnvironments(context, cwd);
        } catch (err) {
            // Treated the same as no environments case
            errorMsg = parseError(err).message;
            ext.outputChannel.appendLog(vscode.l10n.t('Error while getting environments: {0}', errorMsg));
        }

        if (envData.length === 0) {
            await promptCreateNewEnvironment(vscode.l10n.t('There are no environments to select. Would you like to create one?'), errorMsg);
            return; // promptCreateNewEnvironment() will call newEnvironment() asynchronously if necessary
        }

        const envChoices  = envData.map(d => {
            let description: string | undefined;
            if (d.HasLocal && d.HasRemote) {
                description = vscode.l10n.t('local, remote');
            } else if (d.HasLocal) {
                description = vscode.l10n.t('local');
            } else if (d.HasRemote) {
                description = vscode.l10n.t('remote');
            } else {
                description = undefined;
            }

            return {
                label: d.Name,
                data: d,
                description: description,
            } as IAzureQuickPickItem<EnvironmentInfo>;
        });
        const selectedEnv = await context.ui.showQuickPick(envChoices, {
            placeHolder: vscode.l10n.t('Select environment'),
            canPickMany: false,
        });

        name = selectedEnv.data.Name;
    }

    const azureCli = await createAzureDevCli(context);
    const args = composeArgs(
        withArg('env', 'select'),
        withQuotedArg(name),
    )();
    await execAsync(azureCli.invocation, args, azureCli.spawnOptions(cwd));

    void vscode.window.showInformationMessage(
        vscode.l10n.t("'{0}' is now the default environment.", name));

    if (selectedEnvironment) {
        selectedEnvironment?.context.refreshEnvironments();
    }
}

export async function newEnvironment(context: IActionContext, selectedItem?: vscode.Uri | TreeViewModel): Promise<void> {
    const environmentsNode = isTreeViewModel(selectedItem) ? selectedItem.unwrap<AzureDevCliEnvironments>() : undefined;
    const selectedFile = environmentsNode?.context.configurationFile ?? selectedItem as vscode.Uri;
    let folder: vscode.WorkspaceFolder | undefined = (selectedFile ? vscode.workspace.getWorkspaceFolder(selectedFile) : undefined);
    if (!folder) {
        folder = await quickPickWorkspaceFolder(context, vscode.l10n.t("To run '{0}' command you must first open a folder or workspace in VS Code", 'env new'));
    }

    const azureCli = await createAzureDevCli(context);
    const args = composeArgs(
        withArg('env', 'new'),
    )();

    void executeAsTask(azureCli.invocation, args, getAzDevTerminalTitle(), azureCli.spawnOptions(folder.uri.fsPath), {
        focus: true,
        alwaysRunNew: true,
        workspaceFolder: folder,
    }, TelemetryId.EnvNewCli).then(() => {
        if (environmentsNode) {
            environmentsNode.context.refreshEnvironments();
        }
    });
}

export async function refreshEnvironment(context: IActionContext, selectedItem?: vscode.Uri | TreeViewModel): Promise<void> {
    const selectedEnvironment = isTreeViewModel(selectedItem) ? selectedItem.unwrap<AzureDevCliEnvironment>() : undefined;
    const selectedFile = isTreeViewModel(selectedItem) ? selectedItem.unwrap<AzureDevCliEnvironments>().context.configurationFile : selectedItem;
    let folder: vscode.WorkspaceFolder | undefined = (selectedFile ? vscode.workspace.getWorkspaceFolder(selectedFile) : undefined);
    if (!folder) {
        folder = await quickPickWorkspaceFolder(context, vscode.l10n.t("To run '{0}' command you must first open a folder or workspace in VS Code", 'env refresh'));
    }

    const azureCli = await createAzureDevCli(context);
    const args = composeArgs(
        withArg('env', 'refresh'),
        withNamedArg('--environment', selectedEnvironment?.name, { shouldQuote: true }),
    )();

    void executeAsTask(azureCli.invocation, args, getAzDevTerminalTitle(), azureCli.spawnOptions(folder.uri.fsPath), {
        focus: true,
        alwaysRunNew: true,
        workspaceFolder: folder,
    }, TelemetryId.EnvRefreshCli).then(() => {
        if (selectedEnvironment) {
            selectedEnvironment.context.refreshEnvironments();
        }
    });
}

export async function listEnvironments(context: IActionContext, selectedFile?: vscode.Uri): Promise<void> {
    let folder: vscode.WorkspaceFolder | undefined = (selectedFile ? vscode.workspace.getWorkspaceFolder(selectedFile) : undefined);
    if (!folder) {
        folder = await quickPickWorkspaceFolder(context, vscode.l10n.t("To run '{0}' command you must first open a folder or workspace in VS Code", 'env list'));
    }

    const azureCli = await createAzureDevCli(context);
    const args = composeArgs(
        withArg('env', 'list'),
    )();

    void executeAsTask(azureCli.invocation, args, getAzDevTerminalTitle(), azureCli.spawnOptions(folder.uri.fsPath), {
        focus: true,
        alwaysRunNew: true,
        workspaceFolder: folder,
    }, TelemetryId.EnvListCli);
}

async function promptCreateNewEnvironment(message: string, details?: string): Promise<void> {
    const createNewEnvItem: vscode.MessageItem = {
        title: vscode.l10n.t('Create a new environment'),
        isCloseAffordance: false
    };
    const cancelItem: vscode.MessageItem = {
        title: vscode.l10n.t('Cancel'),
        isCloseAffordance: true
    };
    const selectedItem = await vscode.window.showErrorMessage(message,
        { modal: true, detail: details }, createNewEnvItem, cancelItem);
    if (selectedItem === createNewEnvItem) {
        void vscode.commands.executeCommand('azure-dev.commands.cli.env-new'); // Don't wait
    }
}
