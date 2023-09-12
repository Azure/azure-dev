// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

import { IActionContext, CommandCallback, registerCommand as registerCommandAzUI } from '@microsoft/vscode-azext-utils';

import ext from '../ext';
import { init } from './init';
import { provision } from './provision';
import { deploy } from './deploy';
import { restore } from './restore';
import { packageCli } from './packageCli';
import { up } from './up';
import { down } from './down';
import { monitor } from './monitor';
import { selectEnvironment, newEnvironment, refreshEnvironment, editEnvironment, deleteEnvironment, listEnvironments } from './env';
import { pipelineConfig } from './pipeline';
import { installCli } from './installCli';
import { loginCli } from './loginCli';
import { getDotEnvFilePath } from './getDotEnvFilePath';
import { revealAzureResource, revealAzureResourceGroup } from './azureWorkspace/reveal';
import { disableDevCenterMode, enableDevCenterMode } from './devCenterMode';

export function registerCommands(): void {
    registerActivityCommand('azure-dev.commands.cli.init', init);
    registerActivityCommand('azure-dev.commands.cli.provision', provision);
    registerActivityCommand('azure-dev.commands.cli.deploy', deploy);
    registerActivityCommand('azure-dev.commands.cli.restore', restore);
    registerActivityCommand('azure-dev.commands.cli.package', packageCli);
    registerActivityCommand('azure-dev.commands.cli.up', up);
    registerActivityCommand('azure-dev.commands.cli.down', down);
    registerActivityCommand('azure-dev.commands.cli.monitor', monitor);
    registerActivityCommand('azure-dev.commands.cli.env-delete', deleteEnvironment);
    registerActivityCommand('azure-dev.commands.cli.env-edit', editEnvironment);
    registerActivityCommand('azure-dev.commands.cli.env-select', selectEnvironment);
    registerActivityCommand('azure-dev.commands.cli.env-new', newEnvironment);
    registerActivityCommand('azure-dev.commands.cli.env-refresh', refreshEnvironment);
    registerActivityCommand('azure-dev.commands.cli.env-list', listEnvironments);
    registerActivityCommand('azure-dev.commands.cli.pipeline-config', pipelineConfig);
    registerActivityCommand('azure-dev.commands.cli.install', installCli);
    registerActivityCommand('azure-dev.commands.cli.login', loginCli);

    registerActivityCommand('azure-dev.commands.azureWorkspace.revealAzureResource', revealAzureResource);
    registerActivityCommand('azure-dev.commands.azureWorkspace.revealAzureResourceGroup', revealAzureResourceGroup);

    registerActivityCommand('azure-dev.commands.enableDevCenterMode', enableDevCenterMode);
    registerActivityCommand('azure-dev.commands.disableDevCenterMode', disableDevCenterMode);

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
