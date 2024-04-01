// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

import { AzureWizardExecuteStep } from '@microsoft/vscode-azext-utils';
import { createAzureDevCli } from '../../../utils/azureDevCli';
import { executeAsTask } from '../../../utils/executeAsTask';
import { getAzDevTerminalTitle } from '../../cmdUtil';
import { TelemetryId } from '../../../telemetry/telemetryId';
import { UpWizardContext } from './UpWizardContext';

export class AzdUpStep extends AzureWizardExecuteStep<UpWizardContext> {
    public priority: number = 200;

    public async execute(wizardContext: UpWizardContext): Promise<void> {
        const azureCli = await createAzureDevCli(wizardContext);
        const command = azureCli.commandBuilder
            .withArg('up');

        wizardContext.startTime = Date.now();

        // Wait
        await executeAsTask(command.build(), getAzDevTerminalTitle(), {
            alwaysRunNew: true,
            cwd: wizardContext.workspaceFolder!.uri.fsPath,
            env: azureCli.env
        }, TelemetryId.UpCli);
    }

    public shouldExecute(): boolean {
        return true;
    }
}
