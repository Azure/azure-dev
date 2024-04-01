// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

import * as vscode from 'vscode';
import { AzureWizardPromptStep, IAzureQuickPickItem } from '@microsoft/vscode-azext-utils';
import { InitWizardContext } from './InitWizardContext';
import { AgentQuickPickItem, IAzureAgentInput } from 'vscode-azure-agent-api';
import { VSCodeAzureSubscriptionProvider } from '@microsoft/vscode-azext-azureauth';

export class ChooseSubscriptionStep extends AzureWizardPromptStep<InitWizardContext> {
    public async prompt(wizardContext: InitWizardContext): Promise<void> {
        const ui = wizardContext.ui as IAzureAgentInput;
        const subscriptionProvider = new VSCodeAzureSubscriptionProvider();

        if (!await subscriptionProvider.isSignedIn()) {
            await subscriptionProvider.signIn();
        }

        const subscriptions = await subscriptionProvider.getSubscriptions();
        const subscriptionPicks = subscriptions.map(subscription => { return { agentMetadata: { }, data: subscription.subscriptionId, label: subscription.name } satisfies AgentQuickPickItem<IAzureQuickPickItem<string>>; });

        const selection = await ui.showQuickPick(
            subscriptionPicks,
            {
                placeHolder: vscode.l10n.t('Select a subscription'),
                agentMetadata: {
                    parameterDisplayTitle: vscode.l10n.t('Subscription'),
                    parameterDisplayDescription: vscode.l10n.t('The Azure subscription in which to deploy resources.')
                }
            }
        );

        wizardContext.subscriptionId = selection.data;
    }

    public shouldPrompt(wizardContext: InitWizardContext): boolean {
        return !wizardContext.subscriptionId;
    }
}
