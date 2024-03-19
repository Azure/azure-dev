// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

import type { IActionContext } from '@microsoft/vscode-azext-utils';

type SimpleCommandConfig = {
    type: 'simple';
    name: string;
    commandId: string;
    displayName: string;
    intentDescription: string;
    requiresAzureLogin?: boolean;
};

export async function getAgentCommands(context: IActionContext): Promise<SimpleCommandConfig[]> {
    return [
        {
            type: 'simple',
            name: 'azdInit',
            commandId: 'azure-dev.commands.cli.init',
            displayName: 'Initialize with Azure Developer CLI',
            intentDescription: 'This is best when users ask to set up or initialize their application for Azure.',
            requiresAzureLogin: false,
        },
        {
            type: 'simple',
            name: 'azdUp',
            commandId: 'azure-dev.commands.cli.up',
            displayName: 'Deploy to Azure with Azure Developer CLI',
            intentDescription: 'This is best when users ask to deploy their application to Azure.',
            requiresAzureLogin: true,
        },
    ];
}
