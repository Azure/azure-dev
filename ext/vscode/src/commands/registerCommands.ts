// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License. 

import { IActionContext, CommandCallback, registerCommand as registerCommandAzUI } from '@microsoft/vscode-azext-utils';

import { init } from './init';
import { infraDeploy } from './infraDeploy';
import { deploy } from './appDeploy';
import { restore } from './appRestore';
import { infraDelete } from './infraDelete';
import { up } from './up';
import { monitor } from './appMonitor';
import { selectEnvironment, newEnvironment, refreshEnvironment } from './env';
import { pipelineConfig } from './pipeline';
import { getDotEnvFilePath } from './getDotEnvFilePath';
import ext from '../ext';

export function registerCommands(): void {
    registerActivityCommand('azure-dev.commands.cli.init', init);
    registerActivityCommand('azure-dev.commands.cli.infra-deploy', infraDeploy);
    registerActivityCommand('azure-dev.commands.cli.app-deploy', deploy);
    registerActivityCommand('azure-dev.commands.cli.app-restore', restore);
    registerActivityCommand('azure-dev.commands.cli.infra-delete', infraDelete);
    registerActivityCommand('azure-dev.commands.cli.up', up);
    registerActivityCommand('azure-dev.commands.cli.app-monitor', monitor);
    registerActivityCommand('azure-dev.commands.cli.env-select', selectEnvironment);
    registerActivityCommand('azure-dev.commands.cli.env-new', newEnvironment);
    registerActivityCommand('azure-dev.commands.cli.env-refresh', refreshEnvironment);
    registerActivityCommand('azure-dev.commands.cli.pipeline-config', pipelineConfig);

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
