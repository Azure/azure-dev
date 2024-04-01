// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

import * as vscode from 'vscode';
import { AzureWizardPromptStep } from '@microsoft/vscode-azext-utils';
import { InitWizardContext } from './InitWizardContext';
import { selectApplicationTemplate } from '../../cmdUtil';

export class ChooseTemplateStep extends AzureWizardPromptStep<InitWizardContext> {
    public async prompt(wizardContext: InitWizardContext): Promise<void> {
        // TODO: If the source folder is non-empty, we'll assume they intend to initialize from source
        const filesInWorkspace = await vscode.workspace.fs.readDirectory(wizardContext.workspaceFolder!.uri);
        if (filesInWorkspace.length === 0) {
            wizardContext.fromSource = false;
            const { templateUrl } = await selectApplicationTemplate(wizardContext);
            wizardContext.templateUrl = templateUrl;
        } else {
            wizardContext.fromSource = true;
        }
    }

    public shouldPrompt(wizardContext: InitWizardContext): boolean {
        return wizardContext.templateUrl === undefined && wizardContext.fromSource === undefined;
    }
}
