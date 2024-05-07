// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

import * as vscode from 'vscode';
import { IActionContext, IAzureQuickPickItem } from '@microsoft/vscode-azext-utils';
import { createAzureDevCli } from '../utils/azureDevCli';
import { quickPickWorkspaceFolder } from '../utils/quickPickWorkspaceFolder';
import { executeAsTask } from '../utils/executeAsTask';
import { getAzDevTerminalTitle, selectApplicationTemplate, showReadmeFile } from './cmdUtil';
import { TelemetryId } from '../telemetry/telemetryId';

interface InitCommandOptions {
    templateUrl?: string;
    useExistingSource?: boolean;
    environmentName?: string;
}

/**
 * A tuple representing the arguments that must be passed to the `init` command when executed via {@link vscode.commands.executeCommand}
 */
export type InitCommandArguments = [ vscode.Uri | undefined, vscode.Uri[] | undefined, InitCommandOptions | undefined, boolean? ];

export async function init(context: IActionContext, selectedFile?: vscode.Uri, allSelectedFiles?: vscode.Uri[], options?: InitCommandOptions, fromAgent: boolean = false): Promise<void> {
    context.telemetry.properties.fromAgent = fromAgent.toString();

    let folder: vscode.WorkspaceFolder | undefined = (selectedFile ? vscode.workspace.getWorkspaceFolder(selectedFile) : undefined);
    if (!folder) {
        folder = await quickPickWorkspaceFolder(context, vscode.l10n.t("To run '{0}' command you must first open a folder or workspace in VS Code", 'init'));
    }

    let templateUrl: string | undefined = options?.templateUrl;
    let useExistingSource: boolean = !!options?.useExistingSource;
    if (!templateUrl && !useExistingSource) {
        const useExistingSourceQuickPick: IAzureQuickPickItem<boolean> = {
            label: vscode.l10n.t('Use code in the current directory'),
            data: true
        };
        const useTemplateQuickPick: IAzureQuickPickItem<boolean> = {
            label: vscode.l10n.t('Select a template'),
            data: false
        };

        useExistingSource = (await context.ui.showQuickPick([useExistingSourceQuickPick, useTemplateQuickPick], {
            placeHolder: vscode.l10n.t('How do you want to initialize your app?'),
        })).data;

        if (!useExistingSource) {
            templateUrl = await selectApplicationTemplate(context);
        }
    }

    const azureCli = await createAzureDevCli(context);
    const command = azureCli.commandBuilder
        .withArg('init');

    if (useExistingSource) {
        context.telemetry.properties.useExistingSource = 'true';
        command.withArg('--from-code');
    } else {
        // Telemetry property is set inside selectApplicationTemplate
        command.withNamedArg('-t', {value: templateUrl!, quoting: vscode.ShellQuoting.Strong});
    }

    const workspacePath = folder?.uri;

    if (options?.environmentName) {
        command.withNamedArg('-e', {value: options.environmentName, quoting: vscode.ShellQuoting.Strong});
    }

    // Don't wait
    void executeAsTask(command.build(), getAzDevTerminalTitle(), {
        focus: true,
        alwaysRunNew: true,
        cwd: workspacePath.fsPath,
        env: azureCli.env
    }, TelemetryId.InitCli).then(() => {
        void showReadmeFile(workspacePath);
    });
}
