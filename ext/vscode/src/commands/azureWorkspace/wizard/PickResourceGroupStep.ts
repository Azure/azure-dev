// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

import { IAzureQuickPickItem } from '@microsoft/vscode-azext-utils';
import { localize } from '../../../localize';
import { AzureDevShowProvider, WorkspaceAzureDevShowProvider } from '../../../services/AzureDevShowProvider';
import { parseAzureResourceId } from '../../../utils/parseAzureResourceId';
import { RevealWizardContext } from './PickEnvironmentStep';
import { SkipIfOneStep } from './SkipIfOneStep';

export interface RevealResourceGroupWizardContext extends RevealWizardContext {
    azureResourceId?: string;
}

export class PickResourceGroupStep extends SkipIfOneStep<RevealResourceGroupWizardContext, string> {
    public constructor(
        private readonly showProvider: AzureDevShowProvider = new WorkspaceAzureDevShowProvider()
    ) {
        super(
            localize('azure-dev.commands.azureWorkspace.revealAzureResourceGroup.selectResource', 'Select a resource group'),
            localize('azure-dev.commands.azureWorkspace.revealAzureResource.noResourceGroups', 'No resource groups found')
        );
    }

    public async prompt(context: RevealResourceGroupWizardContext): Promise<void> {
        context.azureResourceId = await this.promptInternal(context);
    }

    public shouldPrompt(context: RevealResourceGroupWizardContext): boolean {
        return !context.azureResourceId;
    }

    protected override async getPicks(context: RevealResourceGroupWizardContext): Promise<IAzureQuickPickItem<string>[]> {
        const showResults = await this.showProvider.getShowResults(context, context.configurationFile, context.environment);

        if (!showResults?.services) {
            return [];
        }
    
        const resourceGroupIds = new Set<string>();

        for (const serviceName of Object.keys(showResults.services)) {
            for (const resourceId of showResults.services[serviceName].target.resourceIds) {
                const { subscription, resourceGroup } = parseAzureResourceId(resourceId);
                resourceGroupIds.add(`/subscriptions/${subscription}/resourceGroups/${resourceGroup}`);
            }
        }

        return Array.from(resourceGroupIds).map(resourceGroupId => {
            const { subscription, resourceGroup } = parseAzureResourceId(resourceGroupId);
            return {
                label: resourceGroup,
                detail: subscription, // TODO: do we want to show subscription ID?
                data: resourceGroupId
            };
        });
    }
}