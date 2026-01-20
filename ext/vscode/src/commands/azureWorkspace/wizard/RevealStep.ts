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
        return !!wizardContext.azureResourceId;
    }

    public async execute(context: RevealResourceWizardContext | RevealResourceGroupWizardContext): Promise<void> {
        const azureResourceId = nonNullProp(context, 'azureResourceId');
        const api = await getAzureResourceExtensionApi();

        // Show the Azure Resources view first to ensure the reveal is visible
        await vscode.commands.executeCommand('azureResourceGroups.focus');

        try {
            // Try to refresh the Azure Resources view to ensure the tree is loaded
            try {
                await vscode.commands.executeCommand('azureResourceGroups.refresh');
                // Delay to allow the tree to fully populate after refresh.
                // The Azure Resources API doesn't expose an event for tree load completion,
                // so we use a delay as a pragmatic workaround.
                await new Promise(resolve => setTimeout(resolve, 1500));
            } catch (refreshError) {
                ext.outputChannel.debug(vscode.l10n.t('Refresh command not available or failed: {0}', refreshError instanceof Error ? refreshError.message : String(refreshError)));
            }

            // Extract subscription and resource group from the resource ID to reveal the RG first
            const resourceIdMatch = azureResourceId.match(/\/subscriptions\/([^/]+)\/resourceGroups\/([^/]+)/i);
            if (resourceIdMatch) {
                const subscriptionId = resourceIdMatch[1];
                const resourceGroupName = resourceIdMatch[2];

                // Try revealing the resource group first to ensure the tree is expanded
                const rgResourceId = `/subscriptions/${subscriptionId}/resourceGroups/${resourceGroupName}`;
                try {
                    await api.resources.revealAzureResource(rgResourceId, { select: false, focus: false, expand: true });
                    // Delay to allow the tree node to expand before revealing the child resource.
                    // The revealAzureResource API returns before the tree UI fully updates.
                    await new Promise(resolve => setTimeout(resolve, 1000));
                } catch (rgError) {
                    ext.outputChannel.debug(vscode.l10n.t('Resource group reveal failed: {0}', rgError instanceof Error ? rgError.message : String(rgError)));
                }
            }

            const result = await api.resources.revealAzureResource(azureResourceId, { select: true, focus: true, expand: true });

            // Note: The focusGroup command to trigger "Focused Resources" view requires internal
            // tree item context that's not accessible through the public API. Users can manually
            // click the zoom-in icon on the resource group if they want the focused view.

            // Try a second time if needed
            if (result === undefined) {
                // Retry delay: the first reveal may fail if the tree hasn't finished loading.
                // A brief delay before retry often succeeds where the first attempt failed.
                await new Promise(resolve => setTimeout(resolve, 1000));
                const secondResult = await api.resources.revealAzureResource(azureResourceId, { select: true, focus: true, expand: true });

                // Try using the openInPortal command as an alternative
                if (secondResult === undefined) {
                    // Try the workspace resource reveal command specific to this view
                    try {
                        await vscode.commands.executeCommand('azureResourceGroups.revealResource', azureResourceId);
                    } catch (altError) {
                        ext.outputChannel.debug(vscode.l10n.t('Alternative reveal also failed: {0}', altError instanceof Error ? altError.message : String(altError)));
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
        } catch (error) {
            ext.outputChannel.error(vscode.l10n.t('Failed to reveal resource: {0}', error instanceof Error ? error.message : String(error)));
            vscode.window.showErrorMessage(vscode.l10n.t('Failed to reveal Azure resource: {0}', error instanceof Error ? error.message : String(error)));
            throw error;
        }
    }
}
