// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.
import { IActionContext } from '@microsoft/vscode-azext-utils';
import { TelemetryId } from '../telemetry/telemetryId';
import { createAzureDevCli, onAzdLoginAttempted } from '../utils/azureDevCli';
import { executeAsTask } from '../utils/executeAsTask';
import { getAzDevTerminalTitle } from './cmdUtil';

/**
 * A tuple representing the arguments that must be passed to the `loginCli` command when executed via {@link vscode.commands.executeCommand}
 */
export type LoginCliCommandArguments = [ boolean? ];

export async function loginCli(context: IActionContext, fromAgent: boolean = false): Promise<void> {
    context.telemetry.properties.fromAgent = fromAgent.toString();

    const azureCli = await createAzureDevCli(context);
    const command = azureCli.commandBuilder.withArgs(['auth', 'login']);

    await executeAsTask(command.build(), getAzDevTerminalTitle(), {
        focus: true,
        alwaysRunNew: true,
        env: azureCli.env
    }, TelemetryId.LoginCli).then(() => {
        onAzdLoginAttempted();
    });
}
