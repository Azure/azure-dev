// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

import * as vscode from 'vscode';
import { AzureWizardExecuteStep } from '@microsoft/vscode-azext-utils';
import { InitWizardContext } from './InitWizardContext';
import { Progress } from 'vscode';
import { fileExists } from '../../../utils/fileUtils';

export class ShowReadmeStep extends AzureWizardExecuteStep<InitWizardContext> {
    public priority: number = 300;

    public async execute(wizardContext: InitWizardContext, progress: Progress<{ message?: string | undefined; increment?: number | undefined; }>): Promise<void> {
        const candidates: string[] = ["README.md", "README.MD", "readme.md"];

        for (const fname of candidates) {
            const fullPath = vscode.Uri.joinPath(wizardContext.workspaceFolder!.uri!, fname);
            if (await fileExists(fullPath)) {
                void vscode.commands.executeCommand('markdown.showPreview', fullPath, { 'sideBySide': false });
                return;
            }
        }
    }

    public shouldExecute(wizardContext: InitWizardContext): boolean {
        return wizardContext.workspaceFolder !== undefined;
    }
}
