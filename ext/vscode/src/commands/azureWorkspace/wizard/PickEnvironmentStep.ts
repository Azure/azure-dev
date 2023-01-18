// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

import { IActionContext, IAzureQuickPickItem } from '@microsoft/vscode-azext-utils';
import * as vscode from 'vscode';
import { localize } from '../../../localize';
import { AzureDevEnvListProvider, WorkspaceAzureDevEnvListProvider } from '../../../services/AzureDevEnvListProvider';
import { SkipIfOneStep } from './SkipIfOneStep';

export interface RevealWizardContext extends IActionContext {
    configurationFile: vscode.Uri;
    environment?: string;
}

// TODO: this may be able to be changed to use a pick experience from @microsoft/vscode-azext-utils
export class PickEnvironmentStep extends SkipIfOneStep<RevealWizardContext, string> {
    public constructor(
        private readonly azureDevEnvListProvider: AzureDevEnvListProvider = new WorkspaceAzureDevEnvListProvider(),
    ) {
        super(
            localize('azure-dev.commands.azureWorkspace.revealAzureResource.selectEnvironment', 'Select an environment'),
            localize('azure-dev.commands.azureWorkspace.revealAzureResource.noEnvironments', 'No environments found')
        );
    }

    public async prompt(context: RevealWizardContext): Promise<void> {
        context.environment = await this.promptInternal(context);
    }

    public shouldPrompt(context: RevealWizardContext): boolean {
        return !context.environment;
    }

    protected override async getPicks(context: RevealWizardContext): Promise<IAzureQuickPickItem<string>[]> {
        const envListResults = await this.azureDevEnvListProvider.getEnvListResults(context, context.configurationFile);
        return envListResults.map(env => {
            return {
                label: env.Name,
                data: env.Name,
            };
        });
    }
}