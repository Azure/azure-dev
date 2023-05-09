// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

import { IAzureQuickPickItem } from '@microsoft/vscode-azext-utils';
import * as vscode from 'vscode';
import { AzureDevShowProvider, WorkspaceAzureDevShowProvider } from '../../../services/AzureDevShowProvider';
import { parseAzureResourceId } from '../../../utils/parseAzureResourceId';
import { RevealWizardContext } from './PickEnvironmentStep';
import { SkipIfOneStep } from './SkipIfOneStep';

const resourceGroupType = 'Microsoft.Resources/resourceGroups';

export interface RevealResourceGroupWizardContext extends RevealWizardContext {
    azureResourceId?: string;
}

export class PickResourceGroupStep extends SkipIfOneStep<RevealResourceGroupWizardContext, string> {
    public constructor(
        private readonly showProvider: AzureDevShowProvider = new WorkspaceAzureDevShowProvider()
    ) {
        super(
            vscode.l10n.t('Select a resource group'),
            vscode.l10n.t('No resource groups found')
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

        if (!showResults?.services && !showResults?.resources) {
            return [];
        }


        if (showResults.resources?.length) {
            // If showResults.resource is available and has items, it is easier and more accurate than parsing RGs out of the services
            return showResults.resources
                .filter(resource => resource.type.toLowerCase() === resourceGroupType.toLowerCase())
                .map(resource => {
                    const { resourceGroup } = parseAzureResourceId(resource.id);
                    return {
                        label: resourceGroup,
                        data: resource.id
                    };
                });
        } else {
            const resourceGroupIds = new Set<string>();
            if (showResults.services) {
                for (const serviceName of Object.keys(showResults.services)) {
                    const service = showResults.services[serviceName];

                    if (!service?.target?.resourceIds) {
                        continue;
                    }

                    for (const resourceId of service.target.resourceIds) {
                        const { subscription, resourceGroup } = parseAzureResourceId(resourceId);
                        resourceGroupIds.add(`/subscriptions/${subscription}/resourceGroups/${resourceGroup}`);
                    }
                }
            }

            return Array.from(resourceGroupIds).map(resourceGroupId => {
                const { resourceGroup } = parseAzureResourceId(resourceGroupId);
                return {
                    label: resourceGroup,
                    data: resourceGroupId
                };
            });
        }
    }
}