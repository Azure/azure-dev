// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

import { AzureWizardExecuteStep, nonNullProp } from '@microsoft/vscode-azext-utils';
import * as vscode from 'vscode';
import { getAzureResourceExtensionApi } from '../../../utils/getAzureResourceExtensionApi';
import { RevealResourceGroupWizardContext } from './PickResourceGroupStep';
import { RevealResourceWizardContext } from './PickResourceStep';

export class RevealStep extends AzureWizardExecuteStep<RevealResourceWizardContext | RevealResourceGroupWizardContext> {
    public readonly priority: number = 100;

    public shouldExecute(wizardContext: RevealResourceWizardContext | RevealResourceGroupWizardContext): boolean {
        const should = !!wizardContext.azureResourceId;
        console.log('[RevealStep] shouldExecute:', should, 'azureResourceId:', wizardContext.azureResourceId);
        return should;
    }

    public async execute(context: RevealResourceWizardContext | RevealResourceGroupWizardContext): Promise<void> {
        console.log('[RevealStep] Starting execute with azureResourceId:', context.azureResourceId);
        const azureResourceId = nonNullProp(context, 'azureResourceId');
        console.log('[RevealStep] Getting Azure Resource Extension API...');
        const api = await getAzureResourceExtensionApi();
        console.log('[RevealStep] API obtained, focusing Azure Resources view...');

        // Show the Azure Resources view first to ensure the reveal is visible
        await vscode.commands.executeCommand('azureResourceGroups.focus');
        console.log('[RevealStep] View focused');

        // Extract provider from resource ID to determine which extension to activate
        const providerMatch = azureResourceId.match(/\/providers\/([^\/]+)/i);
        const provider = providerMatch ? providerMatch[1] : null;
        console.log('[RevealStep] Resource provider:', provider);

        // Activate the appropriate Azure extension based on provider
        if (provider) {
            const extensionMap: Record<string, string> = {
                'Microsoft.App': 'ms-azuretools.vscode-azurecontainerapps',
                'Microsoft.Web': 'ms-azuretools.vscode-azurefunctions',
                'Microsoft.Storage': 'ms-azuretools.vscode-azurestorage',
                'Microsoft.DocumentDB': 'ms-azuretools.azure-cosmos',
            };

            const extensionId = extensionMap[provider];
            if (extensionId) {
                console.log('[RevealStep] Activating extension:', extensionId);
                const ext = vscode.extensions.getExtension(extensionId);
                if (ext && !ext.isActive) {
                    await ext.activate();
                    console.log('[RevealStep] Extension activated');
                    // Give it time to register its tree data provider
                    await new Promise(resolve => setTimeout(resolve, 1000));
                }
            }
        }

        console.log('[RevealStep] Attempting reveal...');

        try {
            // Try to refresh the Azure Resources view to ensure the tree is loaded
            console.log('[RevealStep] Refreshing Azure Resources tree...');
            try {
                await vscode.commands.executeCommand('azureResourceGroups.refresh');
                console.log('[RevealStep] Refresh command executed');
                await new Promise(resolve => setTimeout(resolve, 1500));
            } catch (refreshError) {
                console.log('[RevealStep] Refresh command not available or failed:', refreshError);
            }

            // Extract subscription and resource group from the resource ID
            const resourceIdParts = azureResourceId.match(/\/subscriptions\/([^\/]+)\/resourceGroups\/([^\/]+)/i);
            if (resourceIdParts) {
                const subscriptionId = resourceIdParts[1];
                const resourceGroupName = resourceIdParts[2];
                console.log('[RevealStep] Subscription:', subscriptionId, 'Resource Group:', resourceGroupName);

                // Try revealing the resource group first to ensure the tree is expanded
                const rgResourceId = `/subscriptions/${subscriptionId}/resourceGroups/${resourceGroupName}`;
                console.log('[RevealStep] Revealing resource group first:', rgResourceId);
                try {
                    await api.resources.revealAzureResource(rgResourceId, { select: false, focus: false, expand: true });
                    await new Promise(resolve => setTimeout(resolve, 1000));
                } catch (rgError) {
                    console.log('[RevealStep] Resource group reveal failed:', rgError);
                }
            }

            console.log('[RevealStep] Calling revealAzureResource with options:', { select: true, focus: true, expand: true });
            const result = await api.resources.revealAzureResource(azureResourceId, { select: true, focus: true, expand: true });
            console.log('[RevealStep] revealAzureResource returned:', result);

            // Try a second time if needed
            if (result === undefined) {
                console.log('[RevealStep] First reveal returned undefined, trying again after delay...');
                await new Promise(resolve => setTimeout(resolve, 1000));
                const secondResult = await api.resources.revealAzureResource(azureResourceId, { select: true, focus: true, expand: true });
                console.log('[RevealStep] Second attempt returned:', secondResult);
                
                // Try using the openInPortal command as an alternative
                if (secondResult === undefined) {
                    console.log('[RevealStep] Reveal API not working as expected, trying alternative approach');
                    // Try the workspace resource reveal command specific to this view
                    try {
                        await vscode.commands.executeCommand('azureResourceGroups.revealResource', azureResourceId);
                        console.log('[RevealStep] Alternative reveal command succeeded');
                    } catch (altError) {
                        console.log('[RevealStep] Alternative reveal also failed:', altError);
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

            console.log('[RevealStep] revealAzureResource completed');
        } catch (error) {
            console.error('[RevealStep] Failed to reveal resource:', error);
            console.error('[RevealStep] Error details:', JSON.stringify(error, null, 2));
            // Show error to user
            vscode.window.showErrorMessage(vscode.l10n.t('Failed to reveal Azure resource: {0}', error instanceof Error ? error.message : String(error)));
            throw error;
        }
    }
}
