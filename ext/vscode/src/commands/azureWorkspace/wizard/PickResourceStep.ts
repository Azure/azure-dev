// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

import { IAzureQuickPickItem } from '@microsoft/vscode-azext-utils';
import * as vscode from 'vscode';
import { AzureDevShowProvider, WorkspaceAzureDevShowProvider } from '../../../services/AzureDevShowProvider';
import { parseAzureResourceId } from '../../../utils/parseAzureResourceId';
import { RevealWizardContext } from './PickEnvironmentStep';
import { SkipIfOneStep } from './SkipIfOneStep';

export interface RevealResourceWizardContext extends RevealWizardContext {
    service: string;
    azureResourceId?: string;
}

export class PickResourceStep extends SkipIfOneStep<RevealResourceWizardContext, string> {
    public constructor(
        private readonly showProvider: AzureDevShowProvider = new WorkspaceAzureDevShowProvider()
    ) {
        super(
            vscode.l10n.t('Select a resource'),
            vscode.l10n.t('No resources found for this service in the selected environment')
        );
    }

    public async prompt(context: RevealResourceWizardContext): Promise<void> {
        console.log('[PickResourceStep] Starting prompt for service:', context.service);
        try {
            context.azureResourceId = await this.promptInternal(context);
            console.log('[PickResourceStep] Selected resource:', context.azureResourceId);
        } catch (error) {
            // Log the error and let the wizard framework handle user-facing error display
            console.error('[PickResourceStep] Error during prompt:', error);
            throw error;
        }
    }

    public shouldPrompt(context: RevealResourceWizardContext): boolean {
        const shouldPrompt = !context.azureResourceId;
        console.log('[PickResourceStep] shouldPrompt:', shouldPrompt);
        return shouldPrompt;
    }

    protected override async getPicks(context: RevealResourceWizardContext): Promise<IAzureQuickPickItem<string>[]> {
        console.log('[PickResourceStep] getPicks called for service:', context.service, 'environment:', context.environment);
        try {
            const showResults = await this.showProvider.getShowResults(context, context.configurationFile, context.environment);
            console.log('[PickResourceStep] showResults received:', !!showResults, 'services:', Object.keys(showResults?.services || {}));

            if (!showResults?.services?.[context.service]?.target?.resourceIds) {
                console.log('[PickResourceStep] No resourceIds found for service:', context.service);
                return [];
            }

            const resourceIds = showResults.services[context.service].target.resourceIds;
            console.log('[PickResourceStep] Found', resourceIds.length, 'resources for service:', context.service);
            return resourceIds.map(resourceId => {
                const { resourceName, provider } = parseAzureResourceId(resourceId);
                return {
                    label: resourceName!,
                    detail: provider, // TODO: do we want to show provider?
                    data: resourceId
                };
            });
        } catch (error) {
            // Log the error for diagnostics
            console.error('[PickResourceStep] Failed to get resources:', error);
            // Re-throw to let the wizard handle it
            throw error;
        }
    }
}
