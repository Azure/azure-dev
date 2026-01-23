// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

import { IActionContext, IAzureQuickPickItem, UserCancelledError } from '@microsoft/vscode-azext-utils';
import { composeArgs, withArg, withNamedArg } from '@microsoft/vscode-processutils';
import { createHash } from 'crypto';
import * as path from 'path';
import * as vscode from 'vscode';
import { createAzureDevCli } from '../utils/azureDevCli';
import { execAsync } from '../utils/execAsync';
import { fileExists } from '../utils/fileUtils';
import { isAzureDevCliModel, isTreeViewModel, TreeViewModel } from '../utils/isTreeViewModel';

const AzureYamlGlobPattern: vscode.GlobPattern = '**/[aA][zZ][uU][rR][eE].{[yY][aA][mM][lL],[yY][mM][lL]}';

/**
 * Validates that a URI has a valid fsPath for file system operations.
 * Virtual file systems or certain VS Code contexts may not provide a valid fsPath.
 * @param context The action context
 * @param selectedFile The URI to validate
 * @param selectedItem The original selected item (for error message context)
 * @param commandName The name of the command being executed (for error message)
 * @throws Error if the URI doesn't have a valid fsPath
 */
export function validateFileSystemUri(
    context: IActionContext,
    selectedFile: vscode.Uri | undefined,
    selectedItem: vscode.Uri | TreeViewModel | undefined,
    commandName: string
): void {
    if (selectedFile && selectedFile.fsPath === undefined) {
        context.errorHandling.suppressReportIssue = true;
        const itemType = isTreeViewModel(selectedItem) ? 'TreeViewModel' :
                        isAzureDevCliModel(selectedItem) ? 'AzureDevCliModel' :
                        selectedItem ? 'vscode.Uri' : 'undefined';
        throw new Error(vscode.l10n.t(
            "Unable to determine working folder for {0} command. The selected file has an unsupported URI scheme '{1}' (selectedItem type: {2}). " +
            "Azure Developer CLI commands are not supported in virtual file systems. Please open a local folder or clone the repository locally.",
            commandName,
            selectedFile.scheme,
            itemType
        ));
    }
}

// If the command was invoked with a specific file context, use the file context as the working directory for running Azure developer CLI commands.
// Otherwise search the workspace for "azure.yaml" or "azure.yml" files. If only one is found, use it (i.e. its folder). If more than one is found, ask the user which one to use.
// If at this point we still do not have a working directory, prompt the user to select one.
export async function getWorkingFolder(context: IActionContext, selectedFile?: vscode.Uri): Promise<string> {
    let folderPath = selectedFile ? path.dirname(selectedFile.fsPath) : undefined;

    if (!folderPath) {
        const azureYamlFile = await pickAzureYamlFile(context);
        if (azureYamlFile) {
            folderPath = path.dirname(azureYamlFile.fsPath);
        }
    }

    if (!folderPath) {
        const localFolderUris = await vscode.window.showOpenDialog({
            canSelectFiles: false,
            canSelectFolders: true,
            canSelectMany: false,
            title: vscode.l10n.t('Select application folder')
        });

        if (!localFolderUris || localFolderUris.length === 0) {
            throw new UserCancelledError();
        }

        const folderUri = localFolderUris[0];
        const azureYamlUri = vscode.Uri.joinPath(folderUri, 'azure.yaml');
        const azureYmlUri = vscode.Uri.joinPath(folderUri, 'azure.yml');

        if (!await fileExists(azureYamlUri) && !await fileExists(azureYmlUri)) {
            context.errorHandling.suppressReportIssue = true;
            throw new Error(vscode.l10n.t("The selected folder does not contain 'azure.yaml' or 'azure.yml' file and cannot be used to run Azure Developer CLI commands"));
        }

        folderPath = folderUri.fsPath;
    }

    return folderPath;
}

export async function pickAzureYamlFile(context: IActionContext): Promise<vscode.Uri | undefined> {
    let filePath: vscode.Uri | undefined = undefined;

    const azureYamlFileUris = await vscode.workspace.findFiles(AzureYamlGlobPattern);

    if (azureYamlFileUris && azureYamlFileUris.length > 0) {
        if (azureYamlFileUris.length > 1) {
            const choices: IAzureQuickPickItem<vscode.Uri>[] = azureYamlFileUris.map(u => { return {
                label: u.fsPath,
                data: u
            };});

            const chosenFile = await context.ui.showQuickPick(choices, {
                canPickMany: false,
                suppressPersistence: true,
                placeHolder: vscode.l10n.t("Select configuration file ('azure.yaml' or 'azure.yml') to use for running Azure developer CLI commands")
            });

            filePath = chosenFile.data;
        } else {
            filePath = azureYamlFileUris[0];
        }
    }

    return filePath;
}

export function getAzDevTerminalTitle(): string {
    return vscode.l10n.t('az dev');
}

const UseCustomTemplate: string = 'azure-dev:/template/custom';

export async function selectApplicationTemplate(context: IActionContext): Promise<string> {
    let templateUrl: string = '';

    const azureCli = await createAzureDevCli(context);
    const args = composeArgs(
        withArg('template', 'list'),
        withNamedArg('--output', 'json'),
    )();

    const { stdout } = await execAsync(azureCli.invocation, args, azureCli.spawnOptions());
    const templates = JSON.parse(stdout) as { name: string, description: string, repositoryPath: string }[];
    const choices = templates.map(t => { return { label: t.name, detail: t.description, data: t.repositoryPath } as IAzureQuickPickItem<string>; });
    choices.unshift({ label: vscode.l10n.t('Use another template...'), data: '', id: UseCustomTemplate });

    const template = await context.ui.showQuickPick(choices, {
        canPickMany: false,
        title: vscode.l10n.t('Select application template')
    });

    if (template.id === UseCustomTemplate) {
        templateUrl = await context.ui.showInputBox({
            prompt: vscode.l10n.t("Enter application template repository name ('{org or user}/{repo}')")
        });
    } else {
        templateUrl = template.data;
    }

    context.telemetry.properties.templateUrlHash = sha256(templateUrl.toLowerCase());
    return templateUrl;
}

export type EnvironmentInfo = {
    Name: string,
    IsDefault: boolean,
    HasLocal?: boolean,
    HasRemote?: boolean,
    DotEnvPath: string,
};

export async function getEnvironments(context: IActionContext, cwd: string): Promise<EnvironmentInfo[]> {
    const azureCli = await createAzureDevCli(context);
    const args = composeArgs(
        withArg('env', 'list', '--no-prompt'),
        withNamedArg('--output', 'json'),
    )();

    const { stdout } = await execAsync(azureCli.invocation, args, azureCli.spawnOptions(cwd));
    const envInfo = JSON.parse(stdout) as EnvironmentInfo[];
    context.telemetry.properties.environmentCount = envInfo.length.toString();
    return envInfo;
}

function sha256(s: string): string {
    const hash = createHash('sha256');
    const retval = hash.update(s).digest('hex');
    return retval;
}

export async function showReadmeFile(folder: vscode.Uri | undefined): Promise<void> {
    // The whole action is "best effort" -- if folder/file do not exist, just do nothing.

    if (!folder) {
        return;
    }

    const candidates: string[] = ["README.md", "README.MD", "readme.md"];

    for (const fname of candidates) {
        const fullPath = vscode.Uri.joinPath(folder, fname);
        if (await fileExists(fullPath)) {
            void vscode.commands.executeCommand('markdown.showPreview', fullPath, { 'sideBySide': false });
            return;
        }
    }
}
