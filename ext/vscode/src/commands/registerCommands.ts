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
import { revealAzureResource, revealAzureResourceGroup, showInAzurePortal } from './azureWorkspace/reveal';
import { disableDevCenterMode, enableDevCenterMode } from './devCenterMode';
import { installExtension, uninstallExtension, upgradeExtension } from './extensions';
import { addService } from './addService';
import { initFromCode, initMinimal, initFromTemplate, searchTemplates, openGallery, openReadme, openGitHubRepo } from './templateTools';

export function registerCommands(): void {
    registerActivityCommand('azure-dev.commands.cli.init', init);
    registerActivityCommand('azure-dev.commands.cli.initFromPom', init);
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
    registerActivityCommand('azure-dev.commands.cli.extension-install', installExtension);
    registerActivityCommand('azure-dev.commands.cli.extension-uninstall', uninstallExtension);
    registerActivityCommand('azure-dev.commands.cli.extension-upgrade', upgradeExtension);
    registerActivityCommand('azure-dev.commands.addService', addService);

    registerActivityCommand('azure-dev.commands.azureWorkspace.revealAzureResource', revealAzureResource);
    registerActivityCommand('azure-dev.commands.azureWorkspace.revealAzureResourceGroup', revealAzureResourceGroup);
    registerActivityCommand('azure-dev.commands.azureWorkspace.showInAzurePortal', showInAzurePortal);

    registerActivityCommand('azure-dev.commands.enableDevCenterMode', enableDevCenterMode);
    registerActivityCommand('azure-dev.commands.disableDevCenterMode', disableDevCenterMode);

    registerActivityCommand('azure-dev.views.templateTools.initFromCode', initFromCode);
    registerActivityCommand('azure-dev.views.templateTools.initMinimal', initMinimal);
    registerActivityCommand('azure-dev.views.templateTools.initFromTemplate', initFromTemplate);
    registerActivityCommand('azure-dev.views.templateTools.initFromTemplateInline', initFromTemplate);
    registerActivityCommand('azure-dev.views.templateTools.search', searchTemplates);
    registerActivityCommand('azure-dev.views.templateTools.openGallery', openGallery);
    registerActivityCommand('azure-dev.views.templateTools.openReadme', openReadme);
    registerActivityCommand('azure-dev.views.templateTools.openGitHub', openGitHubRepo);

    // getDotEnvFilePath() is a utility command that does not deserve "user activity" designation.
    registerCommandAzUI('azure-dev.commands.getDotEnvFilePath', getDotEnvFilePath);
}

// registerActivityCommand wraps a command callback with activity recording.
// The command ID is automatically used as the telemetry event name by registerCommandAzUI.
// For CLI task executions, telemetry is separately tracked via executeAsTask() with TelemetryId enum values.
function registerActivityCommand(commandId: string, callback: CommandCallback, debounce?: number, telemetryId?: string): void {
    registerCommandAzUI(
        commandId,
        // eslint-disable-next-line @typescript-eslint/no-explicit-any
        (context: IActionContext, ...args: any[]): any => {
            void ext.activitySvc.recordActivity();
            // eslint-disable-next-line @typescript-eslint/no-unsafe-argument
            return callback(context, ...args);
        },
        debounce,
        telemetryId
    );
}
