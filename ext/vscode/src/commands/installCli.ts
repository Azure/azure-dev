// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

import { DialogResponses, IActionContext } from '@microsoft/vscode-azext-utils';
import * as os from 'os';
import * as vscode from 'vscode';
import { onAzdInstallAttempted } from '../utils/azureDevCli';
import { executeInTerminal } from '../utils/executeInTerminal';
import { isLinux, isMac, isWindows } from '../utils/osUtils';
import { getAzDevTerminalTitle } from './cmdUtil';

const WindowsTerminalCommand = `powershell -ex AllSigned -c "Invoke-RestMethod 'https://aka.ms/install-azd.ps1' | Invoke-Expression"`;
const LinuxTerminalCommand = `curl -fsSL https://aka.ms/install-azd.sh | bash`;
const MacTerminalCommand = LinuxTerminalCommand; // Same as Linux

/**
 * A tuple representing the arguments that must be passed to the `installCli` command when executed via {@link vscode.commands.executeCommand}
 */
export type InstallCliCommandArguments = [ boolean?, boolean? ];

export async function installCli(context: IActionContext, shouldPrompt: boolean = true, fromAgent: boolean = false): Promise<void> {
    context.telemetry.properties.fromAgent = fromAgent.toString();

    if (shouldPrompt) {
        const message = vscode.l10n.t('This will install or update the Azure Developer CLI. Do you want to continue?');
        // Don't need to check the result, if the user chooses cancel a UserCancelledError will be thrown
        await context.ui.showWarningMessage(message, { modal: true }, DialogResponses.yes, DialogResponses.cancel);
    }

    let terminalCommand: string;

    if (isWindows()) {
        terminalCommand = WindowsTerminalCommand;
    } else if (isLinux()) {
        terminalCommand = LinuxTerminalCommand;
    } else if (isMac()) {
        terminalCommand = MacTerminalCommand;
    } else {
        context.errorHandling.suppressReportIssue = true;
        throw new Error(vscode.l10n.t('Unsupported platform: {0}', os.platform()));
    }

    onAzdInstallAttempted();

    // The installation process will be started but not itself awaited, so we won't know the ultimate result
    await executeInTerminal(terminalCommand, { name: getAzDevTerminalTitle() });
}
