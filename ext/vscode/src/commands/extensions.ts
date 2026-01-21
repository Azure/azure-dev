// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

import * as vscode from 'vscode';
import { IActionContext } from '@microsoft/vscode-azext-utils';
import { CommandLineArgs, composeArgs, withArg, withQuotedArg } from '@microsoft/vscode-processutils';
import { createAzureDevCli } from '../utils/azureDevCli';
import { execAsync } from '../utils/execAsync';
import { TelemetryId } from '../telemetry/telemetryId';
import { ExtensionTreeItem } from '../views/extensions/ExtensionsTreeDataProvider';

async function runExtensionCommand(context: IActionContext, args: CommandLineArgs, title: string, telemetryId: TelemetryId): Promise<void> {
    await vscode.window.withProgress({
        location: vscode.ProgressLocation.Notification,
        title: title,
        cancellable: false
    }, async () => {
        const azureCli = await createAzureDevCli(context);
        try {
            const result = await execAsync(azureCli.invocation, args, azureCli.spawnOptions());
            const output = result.stdout + result.stderr;
            
            // Parse output for status messages
            const lines = output.split('\n');
            let message = vscode.l10n.t('Command completed successfully.');
            
            for (const line of lines) {
                const trimmed = line.trim();
                if (trimmed.includes('Skipped:')) {
                    message = trimmed.replace('(-)', '').trim();
                } else if (trimmed.includes('Installed') && !trimmed.includes('SUCCESS')) {
                    message = trimmed;
                } else if (trimmed.includes('Upgraded')) {
                    message = trimmed;
                } else if (trimmed.includes('Uninstalled')) {
                    message = trimmed;
                }
            }

            void vscode.window.showInformationMessage(message);
        } catch (error) {
            void vscode.window.showErrorMessage(vscode.l10n.t('Command failed: {0}', (error as Error).message));
        }
    });
    
    void vscode.commands.executeCommand('azure-dev.views.extensions.refresh');
}

export async function installExtension(context: IActionContext): Promise<void> {
    const registryName = await context.ui.showInputBox({
        prompt: vscode.l10n.t('Enter the registry name (optional)'),
        placeHolder: vscode.l10n.t('Registry Name (Press Enter to skip)'),
        stepName: 'registryName'
    });

    if (registryName) {
        const location = await context.ui.showInputBox({
            prompt: vscode.l10n.t('Enter the registry location (URL)'),
            placeHolder: vscode.l10n.t('https://...'),
            stepName: 'registryLocation'
        });

        if (!location) {
            return;
        }

        const args = composeArgs(
            withArg('extension', 'source', 'add'),
            withArg('--name', registryName),
            withArg('--location', location)
        )();

        await runExtensionCommand(context, args, vscode.l10n.t('Adding extension source...'), TelemetryId.ExtensionSourceAddCli);
    }

    const id = await context.ui.showInputBox({
        prompt: vscode.l10n.t('Enter the ID of the extension to install'),
        placeHolder: vscode.l10n.t('Extension ID')
    });

    const args = composeArgs(
        withArg('extension', 'install'),
        withQuotedArg(id)
    )();

    await runExtensionCommand(context, args, vscode.l10n.t('Installing extension...'), TelemetryId.ExtensionInstallCli);
}

export async function uninstallExtension(context: IActionContext, item?: ExtensionTreeItem): Promise<void> {
    let id = item?.extension.id;

    if (!id) {
        id = await context.ui.showInputBox({
            prompt: vscode.l10n.t('Enter the ID of the extension to uninstall'),
            placeHolder: vscode.l10n.t('Extension ID')
        });
    }

    const args = composeArgs(
        withArg('extension', 'uninstall'),
        withQuotedArg(id)
    )();

    await runExtensionCommand(context, args, vscode.l10n.t('Uninstalling extension...'), TelemetryId.ExtensionUninstallCli);
}

export async function upgradeExtension(context: IActionContext, item?: ExtensionTreeItem): Promise<void> {
    let id = item?.extension.id;

    if (!id) {
        id = await context.ui.showInputBox({
            prompt: vscode.l10n.t('Enter the ID of the extension to upgrade'),
            placeHolder: vscode.l10n.t('Extension ID')
        });
    }

    const args = composeArgs(
        withArg('extension', 'install'),
        withQuotedArg(id),
        withArg('--force')
    )();

    await runExtensionCommand(context, args, vscode.l10n.t('Upgrading extension...'), TelemetryId.ExtensionUpgradeCli);
}
