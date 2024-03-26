// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

import * as vscode from 'vscode';
import { AzureWizardPromptStep } from '@microsoft/vscode-azext-utils';
import { InitWizardContext } from './InitWizardContext';

export class EnvironmentNameStep extends AzureWizardPromptStep<InitWizardContext> {
    public async prompt(wizardContext: InitWizardContext): Promise<void> {
        wizardContext.environmentName = await wizardContext.ui.showInputBox({
            prompt: vscode.l10n.t('Enter an environment name to use'),
        });
    }

    public shouldPrompt(wizardContext: InitWizardContext): boolean {
        return !wizardContext.environmentName;
    }
}
