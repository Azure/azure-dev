// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

import { registerCommand, type IActionContext } from '@microsoft/vscode-azext-utils';
import { SimpleCommandConfig, SkillCommandConfig as SkillCommandConfigAgent } from 'vscode-azure-agent-api';
import { agentInit } from './agentInit';
import { agentUp } from './agentUp';

// eslint-disable-next-line @typescript-eslint/no-explicit-any
type AzdCommand = { handler: (context: IActionContext, ...args: any[]) => Promise<any> };
type SkillCommandConfig = SkillCommandConfigAgent & AzdCommand;
type CommandConfig = SimpleCommandConfig | SkillCommandConfig;

export function registerAgentCommands(): void {
    registerCommand('azure-dev.commands.agent.getCommands', getAgentCommands);

    for (const command of agentCommands) {
        if ('handler' in command && typeof command.handler === 'function') {
            registerCommand(command.commandId, command.handler);
        }
    }
}

export function getAgentCommands(context: IActionContext): Promise<CommandConfig[]> {
    return Promise.resolve(agentCommands);
}

const agentCommands: CommandConfig[] = [
    {
        type: 'skill',
        name: 'azdInit',
        commandId: 'azure-dev.commands.cli.init.agent',
        displayName: 'Initialize with Azure Developer CLI',
        intentDescription: 'This is best when users ask to set up or initialize their application for Azure.',
        requiresAzureLogin: false,
        handler: agentInit,
    } satisfies SkillCommandConfig,
    {
        type: 'skill',
        name: 'azdUp',
        commandId: 'azure-dev.commands.cli.up.agent',
        displayName: 'Deploy to Azure with Azure Developer CLI',
        intentDescription: 'This is best when users ask to deploy their application to Azure.',
        requiresAzureLogin: true,
        handler: agentUp,
    } satisfies SkillCommandConfig,
];
