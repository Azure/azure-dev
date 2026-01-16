// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

import { AzureWizardExecuteStep, nonNullProp } from '@microsoft/vscode-azext-utils';
import * as vscode from 'vscode';
import ext from '../../../ext';
import { getAzureResourceExtensionApi } from '../../../utils/getAzureResourceExtensionApi';
import { RevealResourceGroupWizardContext } from './PickResourceGroupStep';
import { RevealResourceWizardContext } from './PickResourceStep';

export class RevealStep extends AzureWizardExecuteStep<RevealResourceWizardContext | RevealResourceGroupWizardContext> {
    public readonly priority: number = 100;

    public shouldExecute(wizardContext: RevealResourceWizardContext | RevealResourceGroupWizardContext): boolean {
        const should = !!wizardContext.azureResourceId;
        ext.outputChannel.appendLog(vscode.l10n.t('RevealStep shouldExecute: {0}, azureResourceId: {1}', should, wizardContext.azureResourceId || 'undefined'));
        return should;
    }

    public async execute(context: RevealResourceWizardContext | RevealResourceGroupWizardContext): Promise<void> {
        ext.outputChannel.appendLog(vscode.l10n.t('RevealStep starting execute with azureResourceId: {0}', context.azureResourceId || 'undefined'));
        const azureResourceId = nonNullProp(context, 'azureResourceId');
        ext.outputChannel.appendLog(vscode.l10n.t('Getting Azure Resource Extension API...'));
        const api = await getAzureResourceExtensionApi();
        ext.outputChannel.appendLog(vscode.l10n.t('API obtained, focusing Azure Resources view...'));

        // Show the Azure Resources view first to ensure the reveal is visible
        await vscode.commands.executeCommand('azureResourceGroups.focus');
        ext.outputChannel.appendLog(vscode.l10n.t('View focused'));

        // Extract provider from resource ID to determine which extension to activate
        const providerMatch = azureResourceId.match(/\/providers\/([^/]+)/i);
        const provider = providerMatch ? providerMatch[1] : null;
        ext.outputChannel.appendLog(vscode.l10n.t('Resource provider: {0}', provider || 'none'));

        // Activate the appropriate Azure extension based on provider
        if (provider) {
            const extensionMap: Record<string, string> = {
                // eslint-disable-next-line @typescript-eslint/naming-convention
                'Microsoft.App': 'ms-azuretools.vscode-azurecontainerapps',
                // eslint-disable-next-line @typescript-eslint/naming-convention
                'Microsoft.Web': 'ms-azuretools.vscode-azurefunctions',
                // eslint-disable-next-line @typescript-eslint/naming-convention
                'Microsoft.Storage': 'ms-azuretools.vscode-azurestorage',
                // eslint-disable-next-line @typescript-eslint/naming-convention
                'Microsoft.DocumentDB': 'ms-azuretools.azure-cosmos',
            };

            const extensionId = extensionMap[provider];
            if (extensionId) {
                ext.outputChannel.appendLog(vscode.l10n.t('Activating extension: {0}', extensionId));
                const extension = vscode.extensions.getExtension(extensionId);
                if (extension && !extension.isActive) {
                    await extension.activate();
                    ext.outputChannel.appendLog(vscode.l10n.t('Extension activated'));
                    // Delay to allow the extension to register its tree data provider.
                    // The Azure Resources API doesn't provide an event for when provider registration completes.
                    await new Promise(resolve => setTimeout(resolve, 1000));
                }
            }
        }

        ext.outputChannel.appendLog(vscode.l10n.t('Attempting reveal...'));

        try {
            // Try to refresh the Azure Resources view to ensure the tree is loaded
            ext.outputChannel.appendLog(vscode.l10n.t('Refreshing Azure Resources tree...'));
            try {
                await vscode.commands.executeCommand('azureResourceGroups.refresh');
                ext.outputChannel.appendLog(vscode.l10n.t('Refresh command executed'));
                // Delay to allow the tree to fully populate after refresh.
                // The Azure Resources API doesn't expose an event for tree load completion,
                // so we use a delay as a pragmatic workaround.
                await new Promise(resolve => setTimeout(resolve, 1500));
            } catch (refreshError) {
                ext.outputChannel.appendLog(vscode.l10n.t('Refresh command not available or failed: {0}', refreshError instanceof Error ? refreshError.message : String(refreshError)));
            }

            // Extract subscription and resource group from the resource ID to reveal the RG first
            const resourceIdMatch = azureResourceId.match(/\/subscriptions\/([^/]+)\/resourceGroups\/([^/]+)/i);
            if (resourceIdMatch) {
                const subscriptionId = resourceIdMatch[1];
                const resourceGroupName = resourceIdMatch[2];
                ext.outputChannel.appendLog(vscode.l10n.t('Subscription: {0}, Resource Group: {1}', subscriptionId, resourceGroupName));

                // Try revealing the resource group first to ensure the tree is expanded
                const rgResourceId = `/subscriptions/${subscriptionId}/resourceGroups/${resourceGroupName}`;
                ext.outputChannel.appendLog(vscode.l10n.t('Revealing resource group first: {0}', rgResourceId));
                try {
                    await api.resources.revealAzureResource(rgResourceId, { select: false, focus: false, expand: true });
                    // Delay to allow the tree node to expand before revealing the child resource.
                    // The revealAzureResource API returns before the tree UI fully updates.
                    await new Promise(resolve => setTimeout(resolve, 1000));
                } catch (rgError) {
                    ext.outputChannel.appendLog(vscode.l10n.t('Resource group reveal failed: {0}', rgError instanceof Error ? rgError.message : String(rgError)));
                }
            }

            ext.outputChannel.appendLog(vscode.l10n.t('Calling revealAzureResource with options: select=true, focus=true, expand=true'));
            const result = await api.resources.revealAzureResource(azureResourceId, { select: true, focus: true, expand: true });
            ext.outputChannel.appendLog(vscode.l10n.t('revealAzureResource returned: {0}', String(result)));

            // Note: The focusGroup command to trigger "Focused Resources" view requires internal
            // tree item context that's not accessible through the public API. Users can manually
            // click the zoom-in icon on the resource group if they want the focused view.

            // Try a second time if needed
            if (result === undefined) {
                ext.outputChannel.appendLog(vscode.l10n.t('First reveal returned undefined, trying again after delay...'));
                // Retry delay: the first reveal may fail if the tree hasn't finished loading.
                // A brief delay before retry often succeeds where the first attempt failed.
                await new Promise(resolve => setTimeout(resolve, 1000));
                const secondResult = await api.resources.revealAzureResource(azureResourceId, { select: true, focus: true, expand: true });
                ext.outputChannel.appendLog(vscode.l10n.t('Second attempt returned: {0}', String(secondResult)));

                // Try using the openInPortal command as an alternative
                if (secondResult === undefined) {
                    ext.outputChannel.appendLog(vscode.l10n.t('Reveal API not working as expected, trying alternative approach'));
                    // Try the workspace resource reveal command specific to this view
                    try {
                        await vscode.commands.executeCommand('azureResourceGroups.revealResource', azureResourceId);
                        ext.outputChannel.appendLog(vscode.l10n.t('Alternative reveal command succeeded'));
                    } catch (altError) {
                        ext.outputChannel.appendLog(vscode.l10n.t('Alternative reveal also failed: {0}', altError instanceof Error ? altError.message : String(altError)));
                        vscode.window.showInformationMessage(
                            vscode.l10n.t('Unable to automatically reveal resource in tree. Resource ID: {0}', azureResourceId),
                            vscode.l10n.t('Copy Resource ID'),
                            vscode.l10n.t('Open in Portal')
                        ).then(async selection => {
                            if (selection === vscode.l10n.t('Copy Resource ID')) {
                                await vscode.env.clipboard.writeText(azureResourceId);
                            } else if (selection === vscode.l10n.t('Open in Portal')) {
                                await vscode.commands.executeCommand('azureResourceGroups.openInPortal', azureResourceId);
                            }
                        });
                    }
                }
            }

            ext.outputChannel.appendLog(vscode.l10n.t('revealAzureResource completed'));
        } catch (error) {
            ext.outputChannel.appendLog(vscode.l10n.t('Failed to reveal resource: {0}', error instanceof Error ? error.message : String(error)));
            // Show error to user
            vscode.window.showErrorMessage(vscode.l10n.t('Failed to reveal Azure resource: {0}', error instanceof Error ? error.message : String(error)));
            throw error;
        }
    }
}
