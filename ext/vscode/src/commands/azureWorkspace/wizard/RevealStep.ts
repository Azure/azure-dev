// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

import { AzureWizardExecuteStep, nonNullProp } from '@microsoft/vscode-azext-utils';
import * as vscode from 'vscode';
import { getAzureResourceExtensionApi } from '../../../utils/getAzureResourceExtensionApi';
import { RevealResourceGroupWizardContext } from './PickResourceGroupStep';
import { RevealResourceWizardContext } from './PickResourceStep';

export class RevealStep extends AzureWizardExecuteStep<RevealResourceWizardContext | RevealResourceGroupWizardContext> {
    public readonly priority: number = 100;

    public constructor(private readonly getApiFunction = getAzureResourceExtensionApi) {
        super();
    }

    public shouldExecute(wizardContext: RevealResourceWizardContext | RevealResourceGroupWizardContext): boolean {
        return !!wizardContext.azureResourceId;
    }

    public async execute(context: RevealResourceWizardContext | RevealResourceGroupWizardContext): Promise<void> {
        // Focus the Azure Resources view first
        await vscode.commands.executeCommand('azureResourceGroups.focus');

        const azureResourceId = nonNullProp(context, 'azureResourceId');
        const api = await this.getApiFunction();
        await api.resources.revealAzureResource(azureResourceId, { select: true, focus: true, expand: true });
    }
}
