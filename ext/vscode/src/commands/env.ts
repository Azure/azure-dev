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
import { isAzureDevCliModel, isTreeViewModel, TreeViewModel } from '../utils/isTreeViewModel';
import { quickPickWorkspaceFolder } from '../utils/quickPickWorkspaceFolder';
import { AzureDevCliEnvironments } from '../views/workspace/AzureDevCliEnvironments';
import { AzureDevCliEnvironment } from '../views/workspace/AzureDevCliEnvironment';
import { EnvironmentItem, EnvironmentTreeItem } from '../views/environments/EnvironmentsTreeDataProvider';
import { EnvironmentInfo, getAzDevTerminalTitle, getEnvironments } from './cmdUtil';

export async function editEnvironment(context: IActionContext, selectedEnvironment?: TreeViewModel | EnvironmentTreeItem): Promise<void> {
    if (selectedEnvironment) {
        let environmentFile: vscode.Uri | undefined;

        if (selectedEnvironment instanceof EnvironmentTreeItem) {
            const data = selectedEnvironment.data as EnvironmentItem;
            environmentFile = data.dotEnvPath ? vscode.Uri.file(data.dotEnvPath) : undefined;
        } else {
            const environment = selectedEnvironment.unwrap<AzureDevCliEnvironment>();
            environmentFile = environment.environmentFile;
        }

        if (environmentFile) {
            const document = await vscode.workspace.openTextDocument(environmentFile);
            await vscode.window.showTextDocument(document);
        }
    }
}

export async function deleteEnvironment(context: IActionContext, selectedItem?: vscode.Uri | TreeViewModel | EnvironmentTreeItem): Promise<void> {
    let selectedEnvironment: AzureDevCliEnvironment | undefined;
    let selectedFile: vscode.Uri | undefined;
    let environmentName: string | undefined;

    if (isTreeViewModel(selectedItem)) {
        selectedEnvironment = selectedItem.unwrap<AzureDevCliEnvironment>();
        selectedFile = selectedItem.unwrap<AzureDevCliEnvironments>().context.configurationFile;
    } else if (selectedItem instanceof AzureDevCliEnvironment) {
        selectedEnvironment = selectedItem;
        selectedFile = selectedItem.context.configurationFile;
    } else if (selectedItem instanceof EnvironmentTreeItem) {
        const data = selectedItem.data as EnvironmentItem;
        selectedFile = data.configurationFile;
        environmentName = data.name;
    } else if (isAzureDevCliModel(selectedItem)) {
        selectedFile = selectedItem.context.configurationFile;
    } else {
        selectedFile = selectedItem as vscode.Uri;
    }

    let folder: vscode.WorkspaceFolder | undefined = (selectedFile ? vscode.workspace.getWorkspaceFolder(selectedFile) : undefined);
    if (!folder) {
        folder = await quickPickWorkspaceFolder(context, vscode.l10n.t("To run '{0}' command you must first open a folder or workspace in VS Code", 'env select'));
    }
    const cwd = folder.uri.fsPath;

    let name = selectedEnvironment?.name ?? environmentName;

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

        // Refresh standalone environments view
        void vscode.commands.executeCommand('azure-dev.views.environments.refresh');

        // Refresh workspace resource view
        void vscode.commands.executeCommand('azureWorkspace.refresh');
    }
}

export async function selectEnvironment(context: IActionContext, selectedItem?: vscode.Uri | TreeViewModel | EnvironmentTreeItem): Promise<void> {
    let selectedEnvironment: AzureDevCliEnvironment | undefined;
    let selectedFile: vscode.Uri | undefined;
    let environmentName: string | undefined;

    if (isTreeViewModel(selectedItem)) {
        selectedEnvironment = selectedItem.unwrap<AzureDevCliEnvironment>();
        selectedFile = selectedItem.unwrap<AzureDevCliEnvironments>().context.configurationFile;
    } else if (selectedItem instanceof AzureDevCliEnvironment) {
        selectedEnvironment = selectedItem;
        selectedFile = selectedItem.context.configurationFile;
    } else if (selectedItem instanceof EnvironmentTreeItem) {
        const data = selectedItem.data as EnvironmentItem;
        selectedFile = data.configurationFile;
        environmentName = data.name;
    } else if (isAzureDevCliModel(selectedItem)) {
        selectedFile = selectedItem.context.configurationFile;
    } else {
        selectedFile = selectedItem as vscode.Uri;
    }

    let folder: vscode.WorkspaceFolder | undefined = (selectedFile ? vscode.workspace.getWorkspaceFolder(selectedFile) : undefined);
    if (!folder) {
        folder = await quickPickWorkspaceFolder(context, vscode.l10n.t("To run '{0}' command you must first open a folder or workspace in VS Code", 'env select'));
    }
    const cwd = folder.uri.fsPath;

    let name = selectedEnvironment?.name ?? environmentName;

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

    // Refresh workspace environments view
    if (selectedEnvironment) {
        selectedEnvironment?.context.refreshEnvironments();
    }

    // Refresh standalone environments view
    void vscode.commands.executeCommand('azure-dev.views.environments.refresh');

    // Refresh workspace resource view
    void vscode.commands.executeCommand('azureWorkspace.refresh');
}

export async function newEnvironment(context: IActionContext, selectedItem?: vscode.Uri | TreeViewModel | EnvironmentTreeItem): Promise<void> {
    let environmentsNode: AzureDevCliEnvironments | undefined;
    let selectedFile: vscode.Uri | undefined;

    if (isTreeViewModel(selectedItem)) {
        environmentsNode = selectedItem.unwrap<AzureDevCliEnvironments>();
        selectedFile = environmentsNode.context.configurationFile;
    } else if (selectedItem instanceof AzureDevCliEnvironments) {
        environmentsNode = selectedItem;
        selectedFile = selectedItem.context.configurationFile;
    } else if (selectedItem instanceof EnvironmentTreeItem) {
        const data = selectedItem.data as EnvironmentItem;
        selectedFile = data.configurationFile;
    } else if (isAzureDevCliModel(selectedItem)) {
        selectedFile = selectedItem.context.configurationFile;
    } else if (selectedItem instanceof vscode.Uri) {
        selectedFile = selectedItem;
    }

    let folder: vscode.WorkspaceFolder | undefined = (selectedFile ? vscode.workspace.getWorkspaceFolder(selectedFile) : undefined);
    if (!folder) {
        folder = await quickPickWorkspaceFolder(context, vscode.l10n.t("To run '{0}' command you must first open a folder or workspace in VS Code", 'env new'));
    }

    // Get current environment
    let currentEnv: string | undefined;
    try {
        const envs = await getEnvironments(context, folder.uri.fsPath);
        currentEnv = envs.find(e => e.IsDefault)?.Name;
    } catch (err) {
        // Ignore error, maybe no environments yet
    }

    const name = await vscode.window.showInputBox({
        prompt: vscode.l10n.t('Enter the name of the new environment'),
        placeHolder: vscode.l10n.t('Environment name'),
        validateInput: (value) => {
            if (!value || value.trim().length === 0) {
                return vscode.l10n.t('Name cannot be empty');
            }
            return undefined;
        }
    });

    if (!name) {
        return;
    }

    let setAsCurrent = true;
    if (currentEnv) {
        const yesItem: IAzureQuickPickItem<boolean> = { label: vscode.l10n.t('Yes'), data: true };
        const noItem: IAzureQuickPickItem<boolean> = { label: vscode.l10n.t('No'), data: false };
        const result = await context.ui.showQuickPick([yesItem, noItem], {
            placeHolder: vscode.l10n.t('Set the new environment as the current environment?'),
            suppressPersistence: true
        });
        setAsCurrent = result.data;
    }

    const azureCli = await createAzureDevCli(context);
    const args = composeArgs(
        withArg('env', 'new'),
        withQuotedArg(name),
        withArg('--no-prompt')
    )();

    await executeAsTask(azureCli.invocation, args, getAzDevTerminalTitle(), azureCli.spawnOptions(folder.uri.fsPath), {
        focus: true,
        alwaysRunNew: true,
        workspaceFolder: folder,
    }, TelemetryId.EnvNewCli);

    if (!setAsCurrent && currentEnv) {
        const selectArgs = composeArgs(
            withArg('env', 'select'),
            withQuotedArg(currentEnv),
        )();
        try {
            await execAsync(azureCli.invocation, selectArgs, azureCli.spawnOptions(folder.uri.fsPath));
        } catch (err) {
            void vscode.window.showErrorMessage(vscode.l10n.t('Failed to switch back to environment "{0}": {1}', currentEnv, parseError(err).message));
        }
    }

    if (environmentsNode) {
        environmentsNode.context.refreshEnvironments();
    }

    // Refresh standalone environments view
    void vscode.commands.executeCommand('azure-dev.views.environments.refresh');

    // Refresh workspace resource view
    void vscode.commands.executeCommand('azureWorkspace.refresh');
}

export async function refreshEnvironment(context: IActionContext, selectedItem?: vscode.Uri | TreeViewModel | EnvironmentTreeItem): Promise<void> {
    let selectedEnvironment: AzureDevCliEnvironment | undefined;
    let selectedFile: vscode.Uri | undefined;
    let environmentName: string | undefined;

    if (isTreeViewModel(selectedItem)) {
        selectedEnvironment = selectedItem.unwrap<AzureDevCliEnvironment>();
        selectedFile = selectedItem.unwrap<AzureDevCliEnvironments>().context.configurationFile;
    } else if (selectedItem instanceof AzureDevCliEnvironment) {
        selectedEnvironment = selectedItem;
        selectedFile = selectedItem.context.configurationFile;
    } else if (selectedItem instanceof EnvironmentTreeItem) {
        const data = selectedItem.data as EnvironmentItem;
        selectedFile = data.configurationFile;
        environmentName = data.name;
    } else if (isAzureDevCliModel(selectedItem)) {
        selectedFile = selectedItem.context.configurationFile;
    } else {
        selectedFile = selectedItem as vscode.Uri;
    }

    let folder: vscode.WorkspaceFolder | undefined = (selectedFile ? vscode.workspace.getWorkspaceFolder(selectedFile) : undefined);
    if (!folder) {
        folder = await quickPickWorkspaceFolder(context, vscode.l10n.t("To run '{0}' command you must first open a folder or workspace in VS Code", 'env refresh'));
    }

    const azureCli = await createAzureDevCli(context);
    const args = composeArgs(
        withArg('env', 'refresh'),
        withNamedArg('--environment', selectedEnvironment?.name ?? environmentName, { shouldQuote: true }),
    )();

    void executeAsTask(azureCli.invocation, args, getAzDevTerminalTitle(), azureCli.spawnOptions(folder.uri.fsPath), {
        focus: true,
        alwaysRunNew: true,
        workspaceFolder: folder,
    }, TelemetryId.EnvRefreshCli).then(() => {
        if (selectedEnvironment) {
            selectedEnvironment.context.refreshEnvironments();
        }
        // Refresh standalone environments view
        void vscode.commands.executeCommand('azure-dev.views.environments.refresh');
        // Refresh workspace resource view
        void vscode.commands.executeCommand('azureWorkspace.refresh');
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
