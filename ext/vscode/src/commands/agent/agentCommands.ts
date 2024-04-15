// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

import * as vscode from 'vscode';
import { registerCommand, type IActionContext } from '@microsoft/vscode-azext-utils';
import { type SimpleCommandConfig, type SkillCommandConfig as SkillCommandConfigAgent, type WizardCommandConfig } from 'vscode-azure-agent-api';
import { azdSkillCommand } from './azdSkillCommand';

// eslint-disable-next-line @typescript-eslint/no-explicit-any
type AzdCommand = { handler: (context: IActionContext, ...args: any[]) => Promise<any> };
type SkillCommandConfig = SkillCommandConfigAgent & AzdCommand;
type CommandConfig = SimpleCommandConfig | SkillCommandConfig | WizardCommandConfig;

export function registerAgentCommands(): void {
    vscode.commands.registerCommand('azure-dev.commands.agent.getCommands', getAgentCommands);

    for (const command of agentCommands) {
        if ('handler' in command && typeof command.handler === 'function') {
            registerCommand(command.commandId, command.handler);
        }
    }
}

function getAgentCommands(): Promise<CommandConfig[]> {
    return Promise.resolve(agentCommands);
}

const agentCommands: CommandConfig[] = [
    {
        type: 'skill',
        name: 'azdSkill',
        commandId: 'azure-dev.commands.agent.skill',
        displayName: 'Azure Developer CLI Skill',
        intentDescription: 'This is best when users ask to set up, initialize, or deploy their application. This is not best when users ask an informational question.',
        requiresAzureLogin: false,
        handler: azdSkillCommand
    } satisfies SkillCommandConfig
];
