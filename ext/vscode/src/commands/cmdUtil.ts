// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

import * as vscode from 'vscode';
import * as path from 'path';
import { createHash } from 'crypto';
import { IActionContext, IAzureQuickPickItem, UserCancelledError } from '@microsoft/vscode-azext-utils';
import { createAzureDevCli } from "../utils/azureDevCli";
import { execAsync } from "../utils/process";
import { fileExists } from '../utils/fileUtils';

const AzureYamlGlobPattern: vscode.GlobPattern = '**/[aA][zZ][uU][rR][eE].[yY][aA][mM][lL]';

// If the command was invoked with a specific file context, use the file context as the working directory for running Azure developer CLI commands.
// Otherwise search the workspace for "azure.yaml" files. If only one is found, use it (i.e. its folder). If more than one is found, ask the user which one to use.
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

        if (!await fileExists(azureYamlUri)) {
            context.errorHandling.suppressReportIssue = true;
            throw new Error(vscode.l10n.t("The selected folder does not contain 'azure.yaml' file and cannot be used to run Azure Developer CLI commands"));
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
                placeHolder: vscode.l10n.t("Select configuration file ('azure.yaml') to use for running Azure developer CLI commands")
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
    const command = azureCli.commandBuilder
        .withArg('template').withArg('list')
        .withArg('--output').withArg('json')
        .build();
    const result = await execAsync(command, azureCli.spawnOptions());
    const templates = JSON.parse(result.stdout) as { name: string, description: string, repositoryPath: string }[];
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
    DotEnvPath: string
};

export async function getEnvironments(context: IActionContext, cwd: string): Promise<EnvironmentInfo[]> {
    const azureCli = await createAzureDevCli(context);
    const command = azureCli.commandBuilder
        .withArg('env').withArg('list')
        .withArg('--no-prompt')
        .withNamedArg('--output', 'json')
        .build();

    const result = await execAsync(command, azureCli.spawnOptions(cwd));
    const envInfo = JSON.parse(result.stdout) as EnvironmentInfo[];
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
