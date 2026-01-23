// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

import { callWithTelemetryAndErrorHandling, IActionContext } from '@microsoft/vscode-azext-utils';
import { CommandLineArgs, getSafeExecPath, StreamSpawnOptions } from '@microsoft/vscode-processutils';
import * as http from 'http';
import * as os from 'os';
import * as vscode from 'vscode';
import { TelemetryId } from '../telemetry/telemetryId';
import { startAuthServer } from './authServer';
import { isAzdCommand } from './azureDevCli';
import { VsCodeAuthenticationCredential } from './VsCodeAuthenticationCredential';

type ExecuteAsTaskSpawnOptions = Pick<StreamSpawnOptions, 'cwd' | 'env'>;

type ExecuteAsTaskOptions = {
    workspaceFolder?: vscode.WorkspaceFolder;
    alwaysRunNew?: boolean;
    suppressErrors?: boolean;
    focus?: boolean;
};

export function executeAsTask(command: string, args: CommandLineArgs, name: string, spawnOptions?: ExecuteAsTaskSpawnOptions, execOptions?: ExecuteAsTaskOptions, telemetryId?: TelemetryId): Promise<void> {
    const runTask = async () => {
        spawnOptions ??= {};
        execOptions ??= {};

        const env = {...spawnOptions.env};

        let useIntegratedAuth = vscode.workspace.getConfiguration('azure-dev').get<boolean>('auth.useIntegratedAuth', false);

        if (!isAzdCommand(command)) {
            useIntegratedAuth = false;
        }

        let authServer: http.Server | undefined;

        if (useIntegratedAuth) {
            const { server, endpoint, key } = await startAuthServer(new VsCodeAuthenticationCredential());

            env.AZD_AUTH_ENDPOINT = endpoint;
            env.AZD_AUTH_KEY = key;
            authServer = server;
        }

        // Turn the env object into one that vscode.Task can consume
        const envForVSCode: Record<string, string> = {};
        for (const key of Object.keys(env)) {
            if (env[key] !== undefined && env[key] !== null) {
                envForVSCode[key] = env[key];
            }
        }

        const task = new vscode.Task(
            { type: 'shell' },
            execOptions.workspaceFolder ?? vscode.TaskScope.Workspace,
            name,
            'Azure Developer',
            new vscode.ShellExecution(
                getSafeExecPath(command, spawnOptions.env?.PATH),
                args,
                {
                    cwd: (spawnOptions.cwd as string) || execOptions.workspaceFolder?.uri?.fsPath || os.homedir(),
                    env: envForVSCode,
                }
            ),
            [] // problemMatchers
        );

        if (execOptions.alwaysRunNew) {
            // If the command should always run in a new task (even if an identical command is still running), add a random value to the definition
            // This will cause a new task to be run even if one with an identical command line is already running
            task.definition.idRandomizer = Math.random();
        }

        if (execOptions.focus) {
            task.presentationOptions = {
                focus: true,
            };
        }

        const taskExecution = await vscode.tasks.executeTask(task);

        const taskEndPromise = new Promise<void>((resolve, reject) => {
            const disposable = vscode.tasks.onDidEndTaskProcess(e => {
                if (e.execution === taskExecution) {
                    authServer?.close();
                    disposable.dispose();

                    if (e.exitCode && !(execOptions?.suppressErrors)) {
                        reject(e.exitCode);
                    }

                    resolve();
                }
            });
        });

        return taskEndPromise;
    };

    if (telemetryId) {
        return callWithTelemetryAndErrorHandling(telemetryId, (ctx: IActionContext) => {
            // Errors will be displayed in the task pane; no need to show them again in a popup.
            ctx.errorHandling.suppressDisplay = true;

            return runTask();
        });
    } else {
        return runTask();
    }
}
