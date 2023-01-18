// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

import { IAzureQuickPickItem } from '@microsoft/vscode-azext-utils';
import { localize } from '../../../localize';
import { RevealWizardContext } from './PickEnvironmentStep';
import { SkipIfOneStep } from './SkipIfOneStep';

export interface RevealResourceGroupWizardContext extends RevealWizardContext {
    azureResourceId?: string;
}

export class PickResourceGroupStep extends SkipIfOneStep<RevealResourceGroupWizardContext, string> {
    public constructor() {
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
        return []; // TODO
    }
}