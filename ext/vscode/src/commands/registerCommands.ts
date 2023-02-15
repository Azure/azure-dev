// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License. 

import { IActionContext, CommandCallback, registerCommand as registerCommandAzUI } from '@microsoft/vscode-azext-utils';

import { init } from './init';
import { provision } from './provision';
import { deploy } from './deploy';
import { restore } from './restore';
import { infraCreate } from './infraCreate';
import { infraDelete } from './infraDelete';
import { up } from './up';
import { down } from './down';
import { monitor } from './monitor';
import { selectEnvironment, newEnvironment, refreshEnvironment, editEnvironment, deleteEnvironment } from './env';
import { pipelineConfig } from './pipeline';
import { installCli } from './installCli';
import { getDotEnvFilePath } from './getDotEnvFilePath';
import { revealAzureResource, revealAzureResourceGroup } from './azureWorkspace/reveal';
import ext from '../ext';

export function registerCommands(): void {
    registerActivityCommand('azure-dev.commands.cli.init', init);
    registerActivityCommand('azure-dev.commands.cli.provision', provision);
    registerActivityCommand('azure-dev.commands.cli.deploy', deploy);
    registerActivityCommand('azure-dev.commands.cli.restore', restore);
    registerActivityCommand('azure-dev.commands.cli.infra-create', infraCreate);
    registerActivityCommand('azure-dev.commands.cli.infra-delete', infraDelete);
    registerActivityCommand('azure-dev.commands.cli.up', up);
    registerActivityCommand('azure-dev.commands.cli.down', down);
    registerActivityCommand('azure-dev.commands.cli.monitor', monitor);
    registerActivityCommand('azure-dev.commands.cli.env-delete', deleteEnvironment);
    registerActivityCommand('azure-dev.commands.cli.env-edit', editEnvironment);
    registerActivityCommand('azure-dev.commands.cli.env-select', selectEnvironment);
    registerActivityCommand('azure-dev.commands.cli.env-new', newEnvironment);
    registerActivityCommand('azure-dev.commands.cli.env-refresh', refreshEnvironment);
    registerActivityCommand('azure-dev.commands.cli.pipeline-config', pipelineConfig);
    registerActivityCommand('azure-dev.commands.cli.install', installCli);

    registerActivityCommand('azure-dev.commands.azureWorkspace.revealAzureResource', revealAzureResource);
    registerActivityCommand('azure-dev.commands.azureWorkspace.revealAzureResourceGroup', revealAzureResourceGroup);

    // getDotEnvFilePath() is a utility command that does not deserve "user activity" designation.
    registerCommandAzUI('azure-dev.commands.getDotEnvFilePath', getDotEnvFilePath);
}

function registerActivityCommand(commandId: string, callback: CommandCallback, debounce?: number, telemetryId?:string): void {
    registerCommandAzUI(
        commandId,
        // eslint-disable-next-line @typescript-eslint/no-explicit-any
        async(context: IActionContext, ...args: any[]) => {
            void ext.activitySvc.recordActivity();
            return callback(context, ...args);
        },
        debounce,
        telemetryId
    );
}
