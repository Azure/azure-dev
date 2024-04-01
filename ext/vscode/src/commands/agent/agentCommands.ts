// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

import * as vscode from 'vscode';
import { AzExtUserInputWithInputQueue, AzureUserInputQueue, IAzureUserInput, callWithTelemetryAndErrorHandling, registerCommand, type IActionContext } from '@microsoft/vscode-azext-utils';
import { SimpleCommandConfig, SkillCommandConfig as SkillCommandConfigAgent, WizardCommandConfig } from 'vscode-azure-agent-api';
import { init } from '../init';
import { up } from '../up';

// eslint-disable-next-line @typescript-eslint/no-explicit-any
type AzdCommand = { handler: (context: IActionContext, ...args: any[]) => Promise<any> };
type SkillCommandConfig = SkillCommandConfigAgent & AzdCommand;
type CommandConfig = SimpleCommandConfig | SkillCommandConfig | WizardCommandConfig;

export function registerAgentCommands(): void {
    vscode.commands.registerCommand('azure-dev.commands.agent.getCommands', getAgentCommands);
    vscode.commands.registerCommand('azure-dev.commands.agent.runWizardCommandWithoutExecution', runWizardCommandWithoutExecution);
    vscode.commands.registerCommand('azure-dev.commands.agent.runWizardCommandWithInputs', runWizardCommandWithInputs);

    for (const command of agentCommands) {
        if ('handler' in command && typeof command.handler === 'function') {
            registerCommand(command.commandId, command.handler);
        }
    }
}

function getAgentCommands(): Promise<CommandConfig[]> {
    return Promise.resolve(agentCommands);
}

async function runWizardCommandWithoutExecution(command: WizardCommandConfig, ui: IAzureUserInput): Promise<void> {
    if (command.commandId === 'azure-dev.commands.cli.init') {
        await callWithTelemetryAndErrorHandling('azure-dev.commands.cli.init.viaAgent', async (context) => {
            return await init({ ...context, ui: ui, skipExecute: true });
        });
    } else if (command.commandId === 'azure-dev.commands.cli.up') {
        await callWithTelemetryAndErrorHandling('azure-dev.commands.cli.up.viaAgent', async (context) => {
            return await up({ ...context, ui: ui, skipExecute: true });
        });
    } else {
        throw new Error('Unknown command: ' + command.commandId);
    }
}

async function runWizardCommandWithInputs(command: WizardCommandConfig, inputsQueue: AzureUserInputQueue): Promise<void> {
    if (command.commandId === 'azure-dev.commands.cli.init') {
        await callWithTelemetryAndErrorHandling('azure-dev.commands.cli.init.viaAgentActual', async (context) => {
            const azureUserInput = new AzExtUserInputWithInputQueue(context, inputsQueue);
            return await init({ ...context, ui: azureUserInput });
        });
    } else if (command.commandId === 'azure-dev.commands.cli.up') {
        await callWithTelemetryAndErrorHandling('azure-dev.commands.cli.up.viaAgentActual', async (context) => {
            const azureUserInput = new AzExtUserInputWithInputQueue(context, inputsQueue);
            return await up({ ...context, ui: azureUserInput });
        });
    } else {
        throw new Error('Unknown command: ' + command.commandId);
    }
}

const agentCommands: CommandConfig[] = [
    {
        type: 'wizard',
        name: 'azdInit',
        commandId: 'azure-dev.commands.cli.init',
        displayName: 'Initialize with Azure Developer CLI',
        intentDescription: 'This is best when users ask to set up or initialize their application for Azure.',
        requiresAzureLogin: false,
    } satisfies WizardCommandConfig,
    {
        type: 'wizard',
        name: 'azdUp',
        commandId: 'azure-dev.commands.cli.up',
        displayName: 'Deploy to Azure with Azure Developer CLI',
        intentDescription: 'This is best when users ask to deploy their application to Azure.',
        requiresAzureLogin: true,
    } satisfies WizardCommandConfig,
];
