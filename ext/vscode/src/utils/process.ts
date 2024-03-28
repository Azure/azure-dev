// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

import * as cp from 'child_process';
import * as http from 'http';
import * as vscode from 'vscode';
import { UserCancelledError } from '@microsoft/vscode-azext-utils';
import { startAuthServer } from './servers/authServer';
import { isAzdCommand } from './azureDevCli';
import { isMac } from './osUtils';
import { VsCodeAuthenticationCredential } from './VsCodeAuthenticationCredential';
import { startPromptServer } from './servers/promptServer';

const DEFAULT_BUFFER_SIZE = 1024 * 1024;

type Progress = (content: string, process: cp.ChildProcess) => void;

type ExecError = Error & { code: unknown, signal: unknown, stdErrHandled: boolean };

export async function spawnAsync(
    command: string,
    options?: cp.SpawnOptions & { stdin?: string },
    onStdout?: Progress,
    stdoutBuffer?: Buffer,
    onStderr?: Progress,
    stderrBuffer?: Buffer,
    token?: vscode.CancellationToken): Promise<void> {

    const configuration = vscode.workspace.getConfiguration('azure-dev');
    let useIntegratedAuth = configuration.get<boolean>('auth.useIntegratedAuth', false);
    let useExternalPrompting = configuration.get<boolean>('useVisualStudioCodeForPrompts', true);

    if (!isAzdCommand(command)) {
        useIntegratedAuth = false;
        useExternalPrompting = false;
    }

    let authServer: http.Server | undefined;
    if (useIntegratedAuth) {
        const { server, endpoint, key } = await startAuthServer(new VsCodeAuthenticationCredential());

        authServer = server;
        options ??= {};
        options.env ??= {};
        options.env['AZD_AUTH_ENDPOINT'] = endpoint;
        options.env['AZD_AUTH_KEY'] = key;
    }

    let promptServer: http.Server | undefined;
    if (useExternalPrompting){
        const { server, endpoint, key } = await startPromptServer();

        promptServer = server;
        options ??= {};
        options.env ??= {};
        options.env['AZD_UI_PROMPT_ENDPOINT'] = endpoint;
        options.env['AZD_UI_PROMPT_KEY'] = key;
    }

    return await new Promise((resolve, reject) => {
        let cancellationListener: vscode.Disposable | undefined;
        let stdoutBytesWritten: number = 0;
        let stderrBytesWritten: number = 0;

        // Without the shell option, it pukes on arguments
        options = options || {};
        options.shell = true;

        fixPathForMacIfNeeded(options);

        const subprocess = cp.spawn(command, options);

        subprocess.on('error', (err) => {
            if (cancellationListener) {
                cancellationListener.dispose();
                cancellationListener = undefined;
            }

            authServer?.close();
            promptServer?.close();

            return reject(err);
        });

        subprocess.on('close', (code, signal) => {
            if (cancellationListener) {
                cancellationListener.dispose();
                cancellationListener = undefined;
            }

            authServer?.close();
            promptServer?.close();

            if (token && token.isCancellationRequested) {
                // If cancellation is requested we'll assume that's why it exited
                return reject(new UserCancelledError());
            } else if (code) {
                let errorMessage = vscode.l10n.t('Process \'{0}\' exited with code {1}', command.length > 50 ? `${command.substring(0, 50)}...` : command, code);

                if (stderrBuffer) {
                    errorMessage += vscode.l10n.t('\nError: {0}', bufferToString(stderrBuffer));
                }

                const error = <ExecError>new Error(errorMessage);

                error.code = code;
                error.signal = signal;
                error.stdErrHandled = onStderr !== null;

                return reject(error);
            }

            return resolve();
        });

        if (options?.stdin) {
            subprocess.stdin?.write(options.stdin);
            subprocess.stdin?.end();
        }

        if (onStdout || stdoutBuffer) {
            subprocess.stdout?.on('data', (chunk: Buffer) => {
                const data = chunk.toString();

                if (onStdout) {
                    onStdout(data, subprocess);
                }

                if (stdoutBuffer) {
                    stdoutBytesWritten += stdoutBuffer.write(data, stdoutBytesWritten);
                }
            });
        }

        if (onStderr || stderrBuffer) {
            subprocess.stderr?.on('data', (chunk: Buffer) => {
                const data = chunk.toString();

                if (onStderr) {
                    onStderr(data, subprocess);
                }

                if (stderrBuffer) {
                    stderrBytesWritten += stderrBuffer.write(data, stderrBytesWritten);
                }
            });
        }

        if (token) {
            cancellationListener = token.onCancellationRequested(() => {
                subprocess.kill();
            });
        }
    });
}

export async function execAsync(command: string, options?: cp.ExecOptions & { stdin?: string }, progress?: (content: string, process: cp.ChildProcess) => void): Promise<{ stdout: string, stderr: string }> {
    const stdoutBuffer = Buffer.alloc(options && options.maxBuffer || DEFAULT_BUFFER_SIZE);
    const stderrBuffer = Buffer.alloc(options && options.maxBuffer || DEFAULT_BUFFER_SIZE);

    await spawnAsync(command, options as cp.CommonOptions, progress, stdoutBuffer, progress, stderrBuffer);

    return {
        stdout: bufferToString(stdoutBuffer),
        stderr: bufferToString(stderrBuffer),
    };
}

function bufferToString(buffer: Buffer): string {
    // Remove non-printing control characters and trailing newlines
    // eslint-disable-next-line no-control-regex
    return buffer.toString().replace(/[\x00-\x09\x0B-\x0C\x0E-\x1F]|\r?\n$/g, '');
}

function fixPathForMacIfNeeded(options: cp.SpawnOptions): void {
    if (!isMac()) {
        // Do nothing: not Mac
        return;
    }

    // Looks for `/usr/local/bin` in the PATH.
    // Must be whole, i.e. the left side must be the beginning of the string or :, and the right side must be the end of the string or :
    // Case-insensitive, because Mac is
    const effectivePath: string = (options?.env?.PATH || process.env.PATH) ?? '';
    if (/(?<=^|:)\/usr\/local\/bin(?=$|:)/i.test(effectivePath)) {
        // Do nothing: PATH already contains `/usr/local/bin`
        return;
    }

    options = options ?? {};
    options.env = options.env ?? { ...process.env };

    // Put `/usr/local/bin` on the PATH at the end
    options.env.PATH = `${options.env.PATH}:/usr/local/bin`;
}
