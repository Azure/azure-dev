// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

import * as vscode from 'vscode';
import { AzExtInputBoxOptions, AzureWizardPromptStep } from '@microsoft/vscode-azext-utils';
import { InitWizardContext } from './InitWizardContext';
import { IAzureAgentInput } from 'vscode-azure-agent-api';

export class EnvironmentNameStep extends AzureWizardPromptStep<InitWizardContext> {
    public async prompt(wizardContext: InitWizardContext): Promise<void> {
        const ui = wizardContext.ui as IAzureAgentInput;

        wizardContext.environmentName = await ui.showInputBox<AzExtInputBoxOptions>({
            prompt: vscode.l10n.t('Enter an environment name to use'),
            agentMetadata: {
                parameterDisplayTitle: 'Environment Name',
                parameterDisplayDescription: 'The name of the environment to create',
            }
        });
    }

    public shouldPrompt(wizardContext: InitWizardContext): boolean {
        return !wizardContext.environmentName;
    }
}
