// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

import { AzureWizardPromptStep } from '@microsoft/vscode-azext-utils';
import { InitWizardContext } from './InitWizardContext';

export class ServiceConfirmationStep extends AzureWizardPromptStep<InitWizardContext> {
    public async prompt(wizardContext: InitWizardContext): Promise<void> {
        throw new Error('Method not implemented.');
    }

    public shouldPrompt(wizardContext: InitWizardContext): boolean {
        return false; // TODO
    }
}
