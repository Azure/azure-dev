// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

import { IActionContext, IAzureQuickPickItem } from '@microsoft/vscode-azext-utils';
import { composeArgs, withArg, withFlagArg, withNamedArg } from '@microsoft/vscode-processutils';
import * as vscode from 'vscode';
import { TelemetryId } from '../telemetry/telemetryId';
import { createAzureDevCli } from '../utils/azureDevCli';
import { executeAsTask } from '../utils/executeAsTask';
import { quickPickWorkspaceFolder } from '../utils/quickPickWorkspaceFolder';
import { getAzDevTerminalTitle, selectApplicationTemplate, showReadmeFile } from './cmdUtil';

interface InitCommandOptions {
    templateUrl?: string;
    useExistingSource?: boolean;
    environmentName?: string;
    suppressReadme?: boolean;
    up?: boolean;
}

/**
 * A tuple representing the arguments that must be passed to the `init` command when executed via {@link vscode.commands.executeCommand}
 */
export type InitCommandArguments = [ vscode.Uri | undefined, vscode.Uri[] | undefined, InitCommandOptions | undefined, boolean? ];

export async function init(context: IActionContext, selectedFile?: vscode.Uri, allSelectedFiles?: vscode.Uri[], options?: InitCommandOptions, fromAgent: boolean = false): Promise<void> {
    context.telemetry.properties.fromAgent = fromAgent.toString();

    let folder: vscode.WorkspaceFolder | undefined = (selectedFile ? vscode.workspace.getWorkspaceFolder(selectedFile) : undefined);
    folder ??= await quickPickWorkspaceFolder(context, vscode.l10n.t("To run '{0}' command you must first open a folder or workspace in VS Code", 'init'));

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
    const args = composeArgs(
        withArg('init'),
        withFlagArg('--from-code', useExistingSource),
        withNamedArg('-t', useExistingSource ? undefined : templateUrl, { shouldQuote: true }),
        withNamedArg('-e', options?.environmentName, { shouldQuote: true }),
        withFlagArg('--up', options?.up),
    )();

    context.telemetry.properties.useExistingSource = useExistingSource.toString();

    // Don't wait
    void executeAsTask(azureCli.invocation, args, getAzDevTerminalTitle(), azureCli.spawnOptions(folder?.uri.fsPath), {
        focus: true,
        alwaysRunNew: true,
        workspaceFolder: folder,
    }, TelemetryId.InitCli).then(() => {
        if (!options?.suppressReadme) {
            void showReadmeFile(folder?.uri);
        }
    });
}
