// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

import { IAzureQuickPickItem } from '@microsoft/vscode-azext-utils';
import { localize } from '../../../localize';
import { AzureDevCliService } from '../../../views/workspace/AzureDevCliService';
import { RevealWizardContext } from './PickEnvironmentStep';
import { SkipIfOneStep } from './SkipIfOneStep';

export interface RevealResourceWizardContext extends RevealWizardContext {
    service: AzureDevCliService;
    azureResourceId?: string;
}

export class PickResourceStep extends SkipIfOneStep<RevealResourceWizardContext, string> {
    public constructor() {
        super(
            localize('azure-dev.commands.azureWorkspace.revealAzureResource.selectResource', 'Select a resource'),
            localize('azure-dev.commands.azureWorkspace.revealAzureResource.noResources', 'No resources found')
        );
    }

    public async prompt(context: RevealResourceWizardContext): Promise<void> {
        context.azureResourceId = await this.promptInternal(context);
    }

    public shouldPrompt(context: RevealResourceWizardContext): boolean {
        return !context.azureResourceId;
    }

    protected override async getPicks(context: RevealResourceWizardContext): Promise<IAzureQuickPickItem<string>[]> {
        return []; // TODO
    }
}