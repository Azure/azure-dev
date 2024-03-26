// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

import * as vscode from 'vscode';
import { AzureWizard, IActionContext } from '@microsoft/vscode-azext-utils';
// import { createAzureDevCli } from '../utils/azureDevCli';
// import { quickPickWorkspaceFolder } from '../utils/quickPickWorkspaceFolder';
// import { executeAsTask } from '../utils/executeAsTask';
// import { getAzDevTerminalTitle, selectApplicationTemplate, showReadmeFile } from './cmdUtil';
// import { TelemetryId } from '../telemetry/telemetryId';
import { InitWizardContext } from './agent/wizard/InitWizardContext';
import { ChooseWorkspaceFolderStep } from './agent/wizard/ChooseWorkspaceFolderStep';
import { ChooseSourceStep } from './agent/wizard/ChooseSourceStep';
import { ChooseTemplateStep } from './agent/wizard/ChooseTemplateStep';
import { EnvironmentNameStep } from './agent/wizard/EnvironmentNameStep';
import { AzdInitStep } from './agent/wizard/AzdInitStep';

export async function init(context: IActionContext, selectedFile?: vscode.Uri, allSelectedFiles?: vscode.Uri, options?: Partial<InitWizardContext> & { skipExecute?: boolean }): Promise<void> {
    const wizardContext = context as InitWizardContext;
    wizardContext.workspaceFolder = options?.workspaceFolder;
    wizardContext.templateUrl = options?.templateUrl;
    wizardContext.fromSource = options?.fromSource;
    wizardContext.environmentName = options?.environmentName;

    const promptSteps = [
        new ChooseWorkspaceFolderStep(),
        new ChooseSourceStep(),
        new ChooseTemplateStep(),
        new EnvironmentNameStep(),
    ];

    const executeSteps = [
        new AzdInitStep(),
    ];

    const wizard = new AzureWizard(
        wizardContext,
        {
            promptSteps,
            executeSteps,
            skipExecute: !!options?.skipExecute,
            title: vscode.l10n.t('Initializing with Azure Developer CLI'),
        }
    );

    await wizard.prompt();
    await wizard.execute();

    // let folder: vscode.WorkspaceFolder | undefined = (selectedFile ? vscode.workspace.getWorkspaceFolder(selectedFile) : undefined);
    // if (!folder) {
    //     folder = await quickPickWorkspaceFolder(context, vscode.l10n.t("To run '{0}' command you must first open a folder or workspace in VS Code", 'init'));
    // }

    // const templateUrl = options?.templateUrl ?? await selectApplicationTemplate(context);

    // const azureCli = await createAzureDevCli(context);
    // const command = azureCli.commandBuilder
    //     .withArg('init')
    //     .withNamedArg('-t', {value: templateUrl, quoting: vscode.ShellQuoting.Strong});
    // const workspacePath = folder?.uri;

    // if (options?.environmentName) {
    //     command.withNamedArg('-e', {value: options.environmentName, quoting: vscode.ShellQuoting.Strong});
    // }

    // // Wait
    // await executeAsTask(command.build(), getAzDevTerminalTitle(), {
    //     alwaysRunNew: true,
    //     cwd: workspacePath.fsPath,
    //     env: azureCli.env
    // }, TelemetryId.InitCli).then(() => {
    //     void showReadmeFile(workspacePath);
    // });
}
