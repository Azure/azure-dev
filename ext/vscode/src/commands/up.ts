// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

import * as vscode from 'vscode';
import { AzureWizard, IActionContext } from "@microsoft/vscode-azext-utils";
import { TreeViewModel } from '../utils/isTreeViewModel';
import { UpWizardContext } from './agent/wizard/UpWizardContext';
import { ChooseWorkspaceFolderStep } from './agent/wizard/ChooseWorkspaceFolderStep';
import { AzdUpStep } from './agent/wizard/AzdUpStep';
import { ToastUpDurationStep } from './agent/wizard/ToastUpDurationStep';

export async function up(context: IActionContext & { skipExecute?: boolean }, selectedItem?: vscode.Uri | TreeViewModel): Promise<void> {
    const wizardContext = context as UpWizardContext;

    const promptSteps = [
        new ChooseWorkspaceFolderStep(),
    ];

    const executeSteps = [
        new AzdUpStep(),
        new ToastUpDurationStep(),
    ];

    const wizard = new AzureWizard(
        wizardContext,
        {
            promptSteps,
            executeSteps,
            skipExecute: !!context.skipExecute,
            title: vscode.l10n.t('Deploying with Azure Developer CLI'),
        }
    );

    await wizard.prompt();
    await wizard.execute();
}
