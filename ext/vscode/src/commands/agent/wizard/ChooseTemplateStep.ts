// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

import { AzureWizardPromptStep } from '@microsoft/vscode-azext-utils';
import { InitWizardContext } from './InitWizardContext';
import { selectApplicationTemplate } from '../../cmdUtil';

export class ChooseTemplateStep extends AzureWizardPromptStep<InitWizardContext> {
    public async prompt(wizardContext: InitWizardContext): Promise<void> {
        wizardContext.templateUrl = await selectApplicationTemplate(wizardContext);
    }

    public shouldPrompt(wizardContext: InitWizardContext): boolean {
        return !wizardContext.templateUrl && !wizardContext.fromSource;
    }
}
