// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

import * as vscode from 'vscode';
import { AzureWizardPromptStep } from '@microsoft/vscode-azext-utils';
import { InitWizardContext } from './InitWizardContext';
import { quickPickWorkspaceFolder } from '../../../utils/quickPickWorkspaceFolder';

export class ChooseWorkspaceFolderStep extends AzureWizardPromptStep<InitWizardContext> {
    public async prompt(wizardContext: InitWizardContext): Promise<void> {
        wizardContext.workspaceFolder = await quickPickWorkspaceFolder(wizardContext, vscode.l10n.t("To run '{0}' command you must first open a folder or workspace in VS Code", 'init'));
    }

    public shouldPrompt(wizardContext: InitWizardContext): boolean {
        return !wizardContext.workspaceFolder;
    }
}
