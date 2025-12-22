// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

import { AzureWizard, IActionContext } from '@microsoft/vscode-azext-utils';
import * as vscode from 'vscode';
import { isTreeViewModel, TreeViewModel } from '../../utils/isTreeViewModel';
import { AzureDevCliApplication } from '../../views/workspace/AzureDevCliApplication';
import { AzureDevCliEnvironment } from '../../views/workspace/AzureDevCliEnvironment';
import { AzureDevCliService } from '../../views/workspace/AzureDevCliService';
import { PickEnvironmentStep } from './wizard/PickEnvironmentStep';
import { PickResourceGroupStep, RevealResourceGroupWizardContext } from './wizard/PickResourceGroupStep';
import { PickResourceStep, RevealResourceWizardContext } from './wizard/PickResourceStep';
import { RevealStep } from './wizard/RevealStep';
import { OpenInPortalStep } from './wizard/OpenInPortalStep';

export async function revealAzureResource(context: IActionContext, treeItem?: TreeViewModel | AzureDevCliService): Promise<void> {
    console.log('[revealAzureResource] Starting...');
    if (!treeItem) {
        throw new Error(vscode.l10n.t('This command must be run from a service item in the Azure Developer CLI view'));
    }

    const selectedItem = isTreeViewModel(treeItem) ? treeItem.unwrap<AzureDevCliService>() : treeItem;
    context.telemetry.properties.revealSource = selectedItem.constructor.name;

    const wizardContext = context as RevealResourceWizardContext;
    wizardContext.configurationFile = selectedItem.context.configurationFile;
    wizardContext.service = selectedItem.name;
    console.log('[revealAzureResource] Service:', selectedItem.name, 'ConfigFile:', selectedItem.context.configurationFile.fsPath);

    const wizard = new AzureWizard(context,
        {
            title: vscode.l10n.t('Show Azure Resource'),
            promptSteps: [
                new PickEnvironmentStep(),
                new PickResourceStep(),
            ],
            executeSteps: [
                new RevealStep(),
            ],
            hideStepCount: true, // Steps are very frequently going to be skipped with a default selection made, so don't show the step count
        }
    );

    console.log('[revealAzureResource] Starting wizard.prompt()...');
    await wizard.prompt();
    console.log('[revealAzureResource] wizard.prompt() completed');

    console.log('[revealAzureResource] Starting wizard.execute()...');
    await wizard.execute();
    console.log('[revealAzureResource] wizard.execute() completed');
}

export async function revealAzureResourceGroup(context: IActionContext, treeItem?: TreeViewModel | AzureDevCliApplication | AzureDevCliEnvironment): Promise<void> {
    if (!treeItem) {
        throw new Error(vscode.l10n.t('This command must be run from an application or environment item in the Azure Developer CLI view'));
    }

    const selectedItem = isTreeViewModel(treeItem) ? treeItem.unwrap<AzureDevCliApplication | AzureDevCliEnvironment>() : treeItem;
    context.telemetry.properties.revealSource = selectedItem.constructor.name;

    const wizardContext = context as RevealResourceGroupWizardContext;
    wizardContext.configurationFile = selectedItem.context.configurationFile;

    if (selectedItem instanceof AzureDevCliEnvironment) {
        wizardContext.environment = selectedItem.name;
    }

    const wizard = new AzureWizard(context,
        {
            title: vscode.l10n.t('Show Azure Resource Group'),
            promptSteps: [
                new PickEnvironmentStep(),
                new PickResourceGroupStep(),
            ],
            executeSteps: [
                new RevealStep(),
            ],
            hideStepCount: true, // Steps are very frequently going to be skipped with a default selection made, so don't show the step count
        }
    );

    console.log('[revealAzureResourceGroup] Starting wizard.prompt()...');
    await wizard.prompt();
    console.log('[revealAzureResourceGroup] wizard.prompt() completed');

    console.log('[revealAzureResourceGroup] Starting wizard.execute()...');
    await wizard.execute();
    console.log('[revealAzureResourceGroup] wizard.execute() completed');
}

export async function showInAzurePortal(context: IActionContext, treeItem?: TreeViewModel | AzureDevCliService): Promise<void> {
    console.log('[showInAzurePortal] Starting...');
    if (!treeItem) {
        throw new Error(vscode.l10n.t('This command must be run from a service item in the Azure Developer CLI view'));
    }

    const selectedItem = isTreeViewModel(treeItem) ? treeItem.unwrap<AzureDevCliService>() : treeItem;
    context.telemetry.properties.showInPortalSource = selectedItem.constructor.name;

    const wizardContext = context as RevealResourceWizardContext;
    wizardContext.configurationFile = selectedItem.context.configurationFile;
    wizardContext.service = selectedItem.name;
    console.log('[showInAzurePortal] Service:', selectedItem.name, 'ConfigFile:', selectedItem.context.configurationFile.fsPath);

    const wizard = new AzureWizard(context,
        {
            title: vscode.l10n.t('Show in Azure Portal'),
            promptSteps: [
                new PickEnvironmentStep(),
                new PickResourceStep(),
            ],
            executeSteps: [
                new OpenInPortalStep(),
            ],
            hideStepCount: true,
        }
    );

    console.log('[showInAzurePortal] Starting wizard.prompt()...');
    await wizard.prompt();
    console.log('[showInAzurePortal] wizard.prompt() completed');

    console.log('[showInAzurePortal] Starting wizard.execute()...');
    await wizard.execute();
    console.log('[showInAzurePortal] wizard.execute() completed');
}
