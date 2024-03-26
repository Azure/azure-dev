// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

import * as vscode from 'vscode';
import { AzureWizardExecuteStep } from '@microsoft/vscode-azext-utils';
import { InitWizardContext } from './InitWizardContext';
import { Progress } from 'vscode';
import { createAzureDevCli } from '../../../utils/azureDevCli';
import { executeAsTask } from '../../../utils/executeAsTask';
import { getAzDevTerminalTitle, showReadmeFile } from '../../cmdUtil';
import { TelemetryId } from '../../../telemetry/telemetryId';

export class AzdInitStep extends AzureWizardExecuteStep<InitWizardContext> {
    public priority: number = 200;

    public async execute(wizardContext: InitWizardContext, progress: Progress<{ message?: string | undefined; increment?: number | undefined; }>): Promise<void> {
        const azureCli = await createAzureDevCli(wizardContext);
        const command = azureCli.commandBuilder
            .withArg('init')
            .withNamedArg('-e', {value: wizardContext.environmentName!, quoting: vscode.ShellQuoting.Strong});
        const workspacePath = wizardContext.workspaceFolder!.uri;

        if (!wizardContext.fromSource) {
            command.withNamedArg('-t', {value: wizardContext.templateUrl!, quoting: vscode.ShellQuoting.Strong});
        }

        // Wait
        await executeAsTask(command.build(), getAzDevTerminalTitle(), {
            alwaysRunNew: true,
            cwd: workspacePath.fsPath,
            env: azureCli.env
        }, TelemetryId.InitCli).then(() => {
            void showReadmeFile(workspacePath);
        });
    }

    public shouldExecute(): boolean {
        return true;
    }
}
