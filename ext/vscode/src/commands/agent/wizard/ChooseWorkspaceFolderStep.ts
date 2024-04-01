// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

import * as vscode from 'vscode';
import { AzureWizardPromptStep } from '@microsoft/vscode-azext-utils';
import { InitWizardContext } from './InitWizardContext';
import { quickPickWorkspaceFolder } from '../../../utils/quickPickWorkspaceFolder';
import { UpWizardContext } from './UpWizardContext';

export class ChooseWorkspaceFolderStep extends AzureWizardPromptStep<InitWizardContext | UpWizardContext> {
    public async prompt(wizardContext: InitWizardContext | UpWizardContext): Promise<void> {
        wizardContext.workspaceFolder = await quickPickWorkspaceFolder(wizardContext, vscode.l10n.t('To run this command you must first open a folder or workspace in VS Code.'));
    }

    public shouldPrompt(wizardContext: InitWizardContext | UpWizardContext): boolean {
        return !wizardContext.workspaceFolder;
    }
}
