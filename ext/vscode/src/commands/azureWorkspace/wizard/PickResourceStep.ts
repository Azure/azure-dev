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
            vscode.l10n.t('No resources found')
        );
    }

    public async prompt(context: RevealResourceWizardContext): Promise<void> {
        context.azureResourceId = await this.promptInternal(context);
    }

    public shouldPrompt(context: RevealResourceWizardContext): boolean {
        return !context.azureResourceId;
    }

    protected override async getPicks(context: RevealResourceWizardContext): Promise<IAzureQuickPickItem<string>[]> {
        const showResults = await this.showProvider.getShowResults(context, context.configurationFile, context.environment);

        if (!showResults?.services?.[context.service]?.target?.resourceIds) {
            return [];
        }

        return showResults.services[context.service].target.resourceIds.map(resourceId => {
            const { resourceName, provider } = parseAzureResourceId(resourceId);
            return {
                // eslint-disable-next-line @typescript-eslint/no-non-null-assertion
                label: resourceName!,
                detail: provider, // TODO: do we want to show provider?
                data: resourceId
            };
        });
    }
}