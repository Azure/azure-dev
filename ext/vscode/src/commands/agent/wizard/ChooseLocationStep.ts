// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

import { AzureWizardPromptStep } from '@microsoft/vscode-azext-utils';
import { InitWizardContext } from './InitWizardContext';

export class ChooseLocationStep extends AzureWizardPromptStep<InitWizardContext> {
    public async prompt(wizardContext: InitWizardContext): Promise<void> {
        // TODO: right now we always choose EastUS2
        wizardContext.location = 'eastus2';
    }

    public shouldPrompt(wizardContext: InitWizardContext): boolean {
        return !wizardContext.location;
    }
}
