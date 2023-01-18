// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

import { AzureWizard, IActionContext } from '@microsoft/vscode-azext-utils';
import { localize } from '../../localize';
import { AzureDevCliApplication } from '../../views/workspace/AzureDevCliApplication';
import { AzureDevCliEnvironment } from '../../views/workspace/AzureDevCliEnvironment';
import { AzureDevCliService } from '../../views/workspace/AzureDevCliService';
import { PickEnvironmentStep } from './wizard/PickEnvironmentStep';
import { PickResourceGroupStep, RevealResourceGroupWizardContext } from './wizard/PickResourceGroupStep';
import { PickResourceStep, RevealResourceWizardContext } from './wizard/PickResourceStep';
import { RevealStep } from './wizard/RevealStep';

export async function revealAzureResource(context: IActionContext, selectedItem: AzureDevCliService): Promise<void> {
    context.telemetry.properties.revealSource = selectedItem.constructor.name;

    const wizardContext = context as RevealResourceWizardContext;
    wizardContext.configurationFile = selectedItem.context.configurationFile;
    wizardContext.service = selectedItem;

    const wizard = new AzureWizard(context,
        {
            title: localize('azure-dev.commands.azureWorkspace.revealAzureResource.title', 'Show Azure Resource'),
            promptSteps: [
                new PickEnvironmentStep(),
                new PickResourceStep(),
            ],
            executeSteps: [
                new RevealStep(),
            ],
        }
    );

    await wizard.prompt();
    await wizard.execute();
}

export async function revealAzureResourceGroup(context: IActionContext, selectedItem: AzureDevCliApplication | AzureDevCliEnvironment): Promise<void> {
    context.telemetry.properties.revealSource = selectedItem.constructor.name;

    const wizardContext = context as RevealResourceGroupWizardContext;
    wizardContext.configurationFile = selectedItem.context.configurationFile;

    if (selectedItem instanceof AzureDevCliEnvironment) {
        wizardContext.environment = selectedItem.name;
    }

    const wizard = new AzureWizard(context,
        {
            title: localize('azure-dev.commands.azureWorkspace.revealAzureResourceGroup.title', 'Show Azure Resource Group'),
            promptSteps: [
                new PickEnvironmentStep(),
                new PickResourceGroupStep(),
            ],
            executeSteps: [
                new RevealStep(),
            ],
        }
    );

    await wizard.prompt();
    await wizard.execute();
}
