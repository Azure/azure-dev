// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

import { AzureWizard, IActionContext } from '@microsoft/vscode-azext-utils';
import * as vscode from 'vscode';
import { isTreeViewModel, TreeViewModel } from '../../utils/isTreeViewModel';
import { AzureDevCliApplication } from '../../views/workspace/AzureDevCliApplication';
import { AzureDevCliEnvironment } from '../../views/workspace/AzureDevCliEnvironment';
import { AzureDevCliService } from '../../views/workspace/AzureDevCliService';
import { EnvironmentItem, EnvironmentTreeItem } from '../../views/environments/EnvironmentsTreeDataProvider';
import { PickEnvironmentStep } from './wizard/PickEnvironmentStep';
import { PickResourceGroupStep, RevealResourceGroupWizardContext } from './wizard/PickResourceGroupStep';
import { PickResourceStep, RevealResourceWizardContext } from './wizard/PickResourceStep';
import { RevealStep } from './wizard/RevealStep';
import { OpenInPortalStep } from './wizard/OpenInPortalStep';

export async function revealAzureResource(context: IActionContext, treeItem?: TreeViewModel | AzureDevCliService): Promise<void> {
    if (!treeItem) {
        throw new Error(vscode.l10n.t('This command must be run from a service item in the Azure Developer CLI view'));
    }

    const selectedItem = isTreeViewModel(treeItem) ? treeItem.unwrap<AzureDevCliService>() : treeItem;
    context.telemetry.properties.revealSource = selectedItem.constructor.name;

    const wizardContext = context as RevealResourceWizardContext;
    wizardContext.configurationFile = selectedItem.context.configurationFile;
    wizardContext.service = selectedItem.name;

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

    await wizard.prompt();
    await wizard.execute();
}

export async function revealAzureResourceGroup(context: IActionContext, treeItem?: TreeViewModel | AzureDevCliApplication | AzureDevCliEnvironment | EnvironmentTreeItem): Promise<void> {
    if (!treeItem) {
        throw new Error(vscode.l10n.t('This command must be run from an application or environment item in the Azure Developer CLI view'));
    }

    let configurationFile: vscode.Uri;
    let environmentName: string | undefined;

    if (treeItem instanceof EnvironmentTreeItem) {
        const data = treeItem.data as EnvironmentItem;
        configurationFile = data.configurationFile;
        environmentName = data.name;
    } else {
        const selectedItem = isTreeViewModel(treeItem) ? treeItem.unwrap<AzureDevCliApplication | AzureDevCliEnvironment>() : treeItem;
        context.telemetry.properties.revealSource = selectedItem.constructor.name;

        configurationFile = selectedItem.context.configurationFile;
        if (selectedItem instanceof AzureDevCliEnvironment) {
            environmentName = selectedItem.name;
        }
    }

    const wizardContext = context as RevealResourceGroupWizardContext;
    wizardContext.configurationFile = configurationFile;

    if (environmentName) {
        wizardContext.environment = environmentName;
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

    await wizard.prompt();
    await wizard.execute();
}

export async function showInAzurePortal(context: IActionContext, treeItem?: TreeViewModel | AzureDevCliService): Promise<void> {
    if (!treeItem) {
        throw new Error(vscode.l10n.t('This command must be run from a service item in the Azure Developer CLI view'));
    }

    const selectedItem = isTreeViewModel(treeItem) ? treeItem.unwrap<AzureDevCliService>() : treeItem;
    context.telemetry.properties.showInPortalSource = selectedItem.constructor.name;

    const wizardContext = context as RevealResourceWizardContext;
    wizardContext.configurationFile = selectedItem.context.configurationFile;
    wizardContext.service = selectedItem.name;

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

    await wizard.prompt();
    await wizard.execute();
}
