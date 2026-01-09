// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

import { AzureWizardExecuteStep, nonNullProp } from '@microsoft/vscode-azext-utils';
import * as vscode from 'vscode';
import { RevealResourceGroupWizardContext } from './PickResourceGroupStep';
import { RevealResourceWizardContext } from './PickResourceStep';

export class OpenInPortalStep extends AzureWizardExecuteStep<RevealResourceWizardContext | RevealResourceGroupWizardContext> {
    public readonly priority: number = 100;

    public shouldExecute(wizardContext: RevealResourceWizardContext | RevealResourceGroupWizardContext): boolean {
        return !!wizardContext.azureResourceId;
    }

    public async execute(context: RevealResourceWizardContext | RevealResourceGroupWizardContext): Promise<void> {
        const azureResourceId = nonNullProp(context, 'azureResourceId');

        // Construct the Azure Portal URL for the resource
        const portalUrl = `https://portal.azure.com/#@/resource${azureResourceId}`;

        // Open the URL in the default browser
        await vscode.env.openExternal(vscode.Uri.parse(portalUrl));
    }
}
