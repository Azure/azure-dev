// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

import { ChildProcessError, StreamSpawnOptions } from '@microsoft/vscode-processutils';
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

// Simulates a rejection that happens *before* a child process is spawned (e.g. an already-cancelled
// token or a Windows executable-resolution failure). Crucially, the pipes are never ended, so any code
// that awaits stderrFinal.getString() would hang forever.
function fakePreSpawnFailure(rejectWith: Error): SpawnStreamAsync {
    return (_command: string, _args, _options?: StreamSpawnOptions) => {
        return Promise.reject(rejectWith);
    };
}

// Reproduces the real ordering behind this bug: spawnStreamAsync rejects on the process 'exit' event
// *before* the stderr stream has closed. The rejection is returned immediately while the stderr
// write/end is deferred to a later tick, so this only passes if execAsync waits for stderr to close
// after catching the rejection (rather than reading it too early and missing the message).
function fakeSpawnStderrAfterRejection(stderr: string, rejectWith: Error): SpawnStreamAsync {
    return (_command: string, _args, options?: StreamSpawnOptions) => {
        options?.stdOutPipe?.end();

        setTimeout(() => {
            if (stderr) {
                options?.stdErrPipe?.write(Buffer.from(stderr));
            }
            options?.stdErrPipe?.end();
        }, 10);

        return Promise.reject(rejectWith);
    };
}

suite('execAsync Tests', () => {
    test('Returns stdout and stderr on success', async () => {
        const { stdout, stderr } = await execAsync('cmd', [], undefined, fakeSpawn('out', 'err'));

        expect(stdout).to.equal('out');
        expect(stderr).to.equal('err');
    });

    test('Includes process stderr in the error message on non-zero exit', async () => {
        // stderr closes *after* the spawn promise rejects - the real ordering - so this proves execAsync
        // waits for the stderr pipe to close before appending it, instead of reading it too early.
        const spawn = fakeSpawnStderrAfterRejection(
            'ERROR: parsing project file: File is empty.',
            new ChildProcessError('Process exited with code 1', 1, null)
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
        const originalError = new ChildProcessError('Process exited with code 1', 1, null);
        const spawn = fakeSpawn('', '', originalError);

        try {
            await execAsync('cmd', [], undefined, spawn);
            expect.fail('Should have thrown an error');
        } catch (error) {
            expect(error).to.equal(originalError);
            expect((error as Error).message).to.equal('Process exited with code 1');
        }
    });

    test('Rethrows pre-spawn failures immediately without waiting on stderr', async () => {
        const originalError = new Error('Operation cancelled');
        const spawn = fakePreSpawnFailure(originalError);

        // If execAsync awaited stderr for non-ChildProcessError rejections, this would hang because the
        // fake never ends the stderr pipe. A short timeout guards against a regression that reintroduces
        // the hang.
        const timeout = new Promise<never>((_resolve, reject) =>
            setTimeout(() => reject(new Error('execAsync hung waiting on an un-ended stderr pipe')), 1000)
        );

        try {
            await Promise.race([execAsync('cmd', [], undefined, spawn), timeout]);
            expect.fail('Should have thrown an error');
        } catch (error) {
            expect(error, 'Pre-spawn failure should be rethrown unchanged').to.equal(originalError);
        }
    });
});
