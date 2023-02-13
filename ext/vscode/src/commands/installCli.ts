// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

import { DialogResponses, IActionContext } from '@microsoft/vscode-azext-utils';
import * as os from 'os';
import { localize } from '../localize';
import { onAzdInstallAttempted, onAzdInstallCompleted } from '../utils/azureDevCli';
import { executeAsTask } from '../utils/executeAsTask';
import { isLinux, isMac, isWindows } from '../utils/osUtils';
import { getAzDevTerminalTitle } from './cmdUtil';

const WindowsTerminalCommand = `powershell -ex AllSigned -c "Invoke-RestMethod 'https://aka.ms/install-azd.ps1' | Invoke-Expression"`;
const LinuxTerminalCommand = `curl -fsSL https://aka.ms/install-azd.sh | bash`;
const MacTerminalCommand = LinuxTerminalCommand; // Same as Linux

export async function installCli(context: IActionContext, shouldPrompt: boolean = true): Promise<void> {
    // Immediately note that the user has attempted to install to avoid excess prompts
    onAzdInstallAttempted();

    if (shouldPrompt) {
        const message = localize('azure-dev.commands.cli.install.prompt', 'This will install or update the Azure Developer CLI. Do you want to continue?');
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
        throw new Error(localize('azure-dev.commands.cli.install.unsupportedPlatform', 'Unsupported platform: {0}', os.platform()));
    }

    void executeAsTask(terminalCommand, getAzDevTerminalTitle(), { focus: true, alwaysRunNew: true }).finally(() => {
        // Reset the install state so that the next time the user tries to use the CLI, we'll check if it's installed
        onAzdInstallCompleted();
    });
}
