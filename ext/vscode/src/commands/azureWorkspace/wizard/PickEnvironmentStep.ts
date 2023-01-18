// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

import { IActionContext, IAzureQuickPickItem } from '@microsoft/vscode-azext-utils';
import * as vscode from 'vscode';
import { localize } from '../../../localize';
import { SkipIfOneStep } from './SkipIfOneStep';

export interface RevealWizardContext extends IActionContext {
    configurationFile: vscode.Uri;
    environment?: string;
}

export class PickEnvironmentStep extends SkipIfOneStep<RevealWizardContext, string> {
    public constructor() {
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
        return []; // TODO
    }
}