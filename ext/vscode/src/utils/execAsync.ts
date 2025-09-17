// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// This file was lifted and adapted from https://github.com/microsoft/vscode-containers/blob/6a8643df8d42033fb776170dfd0ffe92f316f5b5/src/utils/execAsync.ts

import { AccumulatorStream, CommandLineArgs, NoShell, spawnStreamAsync, StreamSpawnOptions } from '@microsoft/vscode-processutils';

export async function execAsync(command: string, args: CommandLineArgs, options?: Partial<StreamSpawnOptions>): Promise<{ stdout: string, stderr: string }> {
    const stdoutFinal = new AccumulatorStream();
    const stderrFinal = new AccumulatorStream();

    const spawnOptions: StreamSpawnOptions = {
        ...options,
        shellProvider: new NoShell(),
        stdOutPipe: stdoutFinal,
        stdErrPipe: stderrFinal,
    };

    await spawnStreamAsync(command, args, spawnOptions);

    return {
        stdout: await stdoutFinal.getString(),
        stderr: await stderrFinal.getString(),
    };
}
