// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

import { AzureWizardExecuteStep, nonNullProp } from '@microsoft/vscode-azext-utils';
import { getAzureResourceExtensionApi } from '../../../utils/getAzureResourceExtensionApi';
import { RevealResourceGroupWizardContext } from './PickResourceGroupStep';
import { RevealResourceWizardContext } from './PickResourceStep';

export class RevealStep extends AzureWizardExecuteStep<RevealResourceWizardContext | RevealResourceGroupWizardContext> {
    public readonly priority: number = 100;

    public shouldExecute(wizardContext: RevealResourceWizardContext | RevealResourceGroupWizardContext): boolean {
        return true;
    }

    public async execute(context: RevealResourceWizardContext | RevealResourceGroupWizardContext): Promise<void> {
        const azureResourceId = nonNullProp(context, 'azureResourceId');
        const api = await getAzureResourceExtensionApi();
        await api.resources.revealAzureResource(azureResourceId, { select: true, focus: true, expand: true });
    }
}