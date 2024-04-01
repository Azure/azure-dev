// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

import * as vscode from 'vscode';
import { AzureWizard, IActionContext } from '@microsoft/vscode-azext-utils';
// import { createAzureDevCli } from '../utils/azureDevCli';
// import { quickPickWorkspaceFolder } from '../utils/quickPickWorkspaceFolder';
// import { executeAsTask } from '../utils/executeAsTask';
// import { getAzDevTerminalTitle, selectApplicationTemplate, showReadmeFile } from './cmdUtil';
// import { TelemetryId } from '../telemetry/telemetryId';
import { InitWizardContext } from './agent/wizard/InitWizardContext';
import { ChooseWorkspaceFolderStep } from './agent/wizard/ChooseWorkspaceFolderStep';
import { ChooseTemplateStep } from './agent/wizard/ChooseTemplateStep';
import { EnvironmentNameStep } from './agent/wizard/EnvironmentNameStep';
import { AzdInitStep } from './agent/wizard/AzdInitStep';
import { ShowReadmeStep } from './agent/wizard/ShowReadmeStep';
import { ChooseSubscriptionStep } from './agent/wizard/ChooseSubscriptionStep';
import { ChooseLocationStep } from './agent/wizard/ChooseLocationStep';

export async function init(context: IActionContext & { skipExecute?: boolean }, selectedFile?: vscode.Uri, allSelectedFiles?: vscode.Uri, options?: Partial<InitWizardContext>): Promise<void> {
    const wizardContext = context as InitWizardContext;
    wizardContext.workspaceFolder = options?.workspaceFolder;
    wizardContext.templateUrl = options?.templateUrl;
    wizardContext.fromSource = options?.fromSource;
    wizardContext.environmentName = options?.environmentName;

    const promptSteps = [
        new ChooseWorkspaceFolderStep(),
        new ChooseTemplateStep(),
        new EnvironmentNameStep(),
        new ChooseSubscriptionStep(),
        new ChooseLocationStep(),
    ];

    const executeSteps = [
        new AzdInitStep(),
        new ShowReadmeStep(),
    ];

    const wizard = new AzureWizard(
        wizardContext,
        {
            promptSteps,
            executeSteps,
            skipExecute: !!context.skipExecute,
            title: vscode.l10n.t('Initializing with Azure Developer CLI'),
        }
    );

    await wizard.prompt();
    await wizard.execute();
}
