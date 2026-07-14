// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

import { StreamSpawnOptions } from '@microsoft/vscode-processutils';
import { expect } from 'chai';
import { execAsync, SpawnStreamAsync } from '../../../utils/execAsync';

// Builds a fake spawnStreamAsync that emits the given stdout/stderr into the accumulator pipes that
// execAsync wires up, then either resolves or rejects to simulate the process exit. The pipes must be
// ended so that AccumulatorStream.getString() (which awaits the stream 'close' event) resolves - this
// mirrors how a real child process closes its stdio streams on exit.
function fakeSpawn(stdout: string, stderr: string, rejectWith?: Error): SpawnStreamAsync {
    return (_command: string, _args, options?: StreamSpawnOptions) => {
        if (stdout) {
            options?.stdOutPipe?.write(Buffer.from(stdout));
        }
        options?.stdOutPipe?.end();

        if (stderr) {
            options?.stdErrPipe?.write(Buffer.from(stderr));
        }
        options?.stdErrPipe?.end();

        return rejectWith ? Promise.reject(rejectWith) : Promise.resolve();
    };
}

suite('execAsync Tests', () => {
    test('Returns stdout and stderr on success', async () => {
        const { stdout, stderr } = await execAsync('cmd', [], undefined, fakeSpawn('out', 'err'));

        expect(stdout).to.equal('out');
        expect(stderr).to.equal('err');
    });

    test('Includes process stderr in the error message on non-zero exit', async () => {
        const spawn = fakeSpawn(
            '',
            'ERROR: parsing project file: File is empty.',
            new Error('Process exited with code 1')
        );

        try {
            await execAsync('cmd', [], undefined, spawn);
            expect.fail('Should have thrown an error');
        } catch (error) {
            expect(error).to.be.instanceOf(Error);
            // Without this behavior, the message would only be the generic "Process exited with code 1",
            // discarding the real reason emitted by the child process on stderr.
            expect((error as Error).message, 'Error should retain the exit-code context').to.include(
                'Process exited with code 1'
            );
            expect((error as Error).message, 'Error should surface the underlying stderr').to.include(
                'parsing project file: File is empty.'
            );
        }
    });

    test('Rethrows the original error unchanged when stderr is empty', async () => {
        const originalError = new Error('Process exited with code 1');
        const spawn = fakeSpawn('', '', originalError);

        try {
            await execAsync('cmd', [], undefined, spawn);
            expect.fail('Should have thrown an error');
        } catch (error) {
            expect(error).to.equal(originalError);
            expect((error as Error).message).to.equal('Process exited with code 1');
        }
    });
});
