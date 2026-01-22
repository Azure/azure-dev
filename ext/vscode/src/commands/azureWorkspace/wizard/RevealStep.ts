// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

import { AzureWizardExecuteStep, nonNullProp } from '@microsoft/vscode-azext-utils';
import * as vscode from 'vscode';
import { getAzureResourceExtensionApi } from '../../../utils/getAzureResourceExtensionApi';
import { RevealResourceGroupWizardContext } from './PickResourceGroupStep';
import { RevealResourceWizardContext } from './PickResourceStep';

export class RevealStep extends AzureWizardExecuteStep<RevealResourceWizardContext | RevealResourceGroupWizardContext> {
    public readonly priority: number = 100;

    public shouldExecute(wizardContext: RevealResourceWizardContext | RevealResourceGroupWizardContext): boolean {
        return !!wizardContext.azureResourceId;
    }

    public async execute(context: RevealResourceWizardContext | RevealResourceGroupWizardContext): Promise<void> {
        const azureResourceId = nonNullProp(context, 'azureResourceId');
        const api = await getAzureResourceExtensionApi();

        // Focus the Azure Resources view first
        await vscode.commands.executeCommand('azureResourceGroups.focus');

        const result = await api.resources.revealAzureResource(azureResourceId, { select: true, focus: true, expand: true });

        // If reveal failed, show a helpful message with actions
        if (result === undefined) {
            const copyResourceIdOption = vscode.l10n.t('Copy Resource ID');
            const openInPortalOption = vscode.l10n.t('Open in Portal');
            const selection = await vscode.window.showInformationMessage(
                vscode.l10n.t('Unable to reveal resource in tree. Resource ID: {0}', azureResourceId),
                copyResourceIdOption,
                openInPortalOption
            );
            if (selection === copyResourceIdOption) {
                await vscode.env.clipboard.writeText(azureResourceId);
            } else if (selection === openInPortalOption) {
                await vscode.commands.executeCommand('azureResourceGroups.openInPortal', azureResourceId);
            }
        }
    }
}
