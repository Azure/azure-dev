// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

import * as http from 'http';
import * as os from 'os';
import * as vscode from 'vscode';
import { TelemetryId } from '../telemetry/telemetryId';
import { callWithTelemetryAndErrorHandling, IActionContext } from '@microsoft/vscode-azext-utils';
import { startAuthServer } from './authServer';
import { isAzdCommand } from './azureDevCli';
import { VsCodeAuthenticationCredential } from './VsCodeAuthenticationCredential';

type ExecuteAsTaskOptions = {
    workspaceFolder?: vscode.WorkspaceFolder;
    cwd?: string;
    alwaysRunNew?: boolean;
    suppressErrors?: boolean;
    focus?: boolean;
    env?: { [key: string]: string };  // Additional environment settings, merged with parent process environment.
};

export function executeAsTask(command: string, name: string, options?: ExecuteAsTaskOptions, telemetryId?: TelemetryId): Promise<void> {
    const runTask = async () => {
        options ??= {};

        const env = {...options.env};

        let useIntegratedAuth = vscode.workspace.getConfiguration('azure-dev').get<boolean>('auth.useIntegratedAuth', false);

        if (!isAzdCommand(command)) {
            useIntegratedAuth = false;
        }

        let authServer: http.Server | undefined;

        if (useIntegratedAuth) {
            const { server, endpoint, key } = await startAuthServer(new VsCodeAuthenticationCredential());

            env['AZD_AUTH_ENDPOINT'] = endpoint;
            env['AZD_AUTH_KEY'] = key;
            authServer = server;
        }

        const task = new vscode.Task(
            { type: 'shell' },
            options.workspaceFolder ?? vscode.TaskScope.Workspace,
            name,
            'Azure Developer',
            new vscode.ShellExecution(
                command,
                {
                    cwd: options.cwd || options.workspaceFolder?.uri?.fsPath || os.homedir(),
                    env: env,
                }
            ),
            [] // problemMatchers
        );

        if (options.alwaysRunNew) {
            // If the command should always run in a new task (even if an identical command is still running), add a random value to the definition
            // This will cause a new task to be run even if one with an identical command line is already running
            task.definition.idRandomizer = Math.random();
        }

        if (options.focus) {
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

                    if (e.exitCode && !(options?.suppressErrors)) {
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
