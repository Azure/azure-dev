// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

import * as vscode from 'vscode';
import { AzureWizardExecuteStep } from '@microsoft/vscode-azext-utils';
import { UpWizardContext } from './UpWizardContext';

export class ToastUpDurationStep extends AzureWizardExecuteStep<UpWizardContext> {
    public priority: number = 300;

    public async execute(wizardContext: UpWizardContext): Promise<void> {
        const endTime = Date.now();

        const durationMilliseconds = endTime - wizardContext.startTime!;
        const durationSeconds = Math.floor(durationMilliseconds / 1000);
        const durationMinutes = Math.floor(durationSeconds / 60);
        const durationSecondsRemainder = durationSeconds % 60;

        void wizardContext.ui.showWarningMessage(vscode.l10n.t('Deployment to Azure has finished in {0}m {1}s', durationMinutes, durationSecondsRemainder));
    }

    public shouldExecute(): boolean {
        return true;
    }
}
