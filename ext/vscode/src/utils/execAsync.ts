// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// This file was lifted and adapted from https://github.com/microsoft/vscode-containers/blob/6a8643df8d42033fb776170dfd0ffe92f316f5b5/src/utils/execAsync.ts

import { AccumulatorStream, CommandLineArgs, isChildProcessError, NoShell, spawnStreamAsync, StreamSpawnOptions } from '@microsoft/vscode-processutils';

// Alias for the spawn implementation so it can be overridden in tests without spawning a real process.
export type SpawnStreamAsync = typeof spawnStreamAsync;

export async function execAsync(command: string, args: CommandLineArgs, options?: Partial<StreamSpawnOptions>, spawnStreamAsyncFunction: SpawnStreamAsync = spawnStreamAsync): Promise<{ stdout: string, stderr: string }> {
    const stdoutFinal = new AccumulatorStream();
    const stderrFinal = new AccumulatorStream();

    const spawnOptions: StreamSpawnOptions = {
        ...options,
        shellProvider: new NoShell(),
        stdOutPipe: stdoutFinal,
        stdErrPipe: stderrFinal,
    };

    try {
        await spawnStreamAsyncFunction(command, args, spawnOptions);
    } catch (error) {
        // Only a ChildProcessError means the process actually started and exited non-zero, so its
        // stderr pipe was ended and can be read. spawnStreamAsync can also reject *before* spawning
        // (e.g. an already-cancelled token or a Windows executable-resolution failure); in that case
        // stdErrPipe is never ended and getString() would hang forever. Rethrow those immediately.
        if (!isChildProcessError(error)) {
            throw error;
        }

        // On a non-zero exit, spawnStreamAsync rejects with a generic message (e.g. "Process exited
        // with code 1") from within a ChildProcess handler, before the caller ever reads stderr. Append
        // the process's stderr to the error so callers can classify and report the real failure instead
        // of an opaque exit code.
        const stderr = (await stderrFinal.getString()).trim();
        if (stderr && error instanceof Error) {
            error.message = `${error.message}\n${stderr}`.trim();
        }

        throw error;
    }

    return {
        stdout: await stdoutFinal.getString(),
        stderr: await stderrFinal.getString(),
    };
}
