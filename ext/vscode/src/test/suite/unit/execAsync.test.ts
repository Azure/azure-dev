// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

import { expect } from 'chai';
import { execAsync } from '../../../utils/execAsync';

// Use the current Node.js executable to run tiny, cross-platform scripts. This exercises the real
// spawnStreamAsync code path (rather than a mock) so we verify how execAsync surfaces process failures.
const node = process.execPath;

suite('execAsync Tests', () => {
    test('Returns stdout and stderr on success', async () => {
        const { stdout, stderr } = await execAsync(node, [
            '-e',
            'process.stdout.write("out"); process.stderr.write("err")',
        ]);

        expect(stdout).to.equal('out');
        expect(stderr).to.equal('err');
    });

    test('Includes process stderr in the error message on non-zero exit', async () => {
        try {
            await execAsync(node, [
                '-e',
                'process.stderr.write("ERROR: parsing project file: File is empty."); process.exit(1)',
            ]);
            expect.fail('Should have thrown an error');
        } catch (error) {
            expect(error).to.be.instanceOf(Error);
            // Without this behavior, the message would only be the generic "Process exited with code 1",
            // discarding the real reason emitted by the child process on stderr.
            expect((error as Error).message, 'Error should surface the underlying stderr').to.include(
                'parsing project file: File is empty.'
            );
        }
    });

    test('Rejects on non-zero exit even when stderr is empty', async () => {
        try {
            await execAsync(node, ['-e', 'process.exit(1)']);
            expect.fail('Should have thrown an error');
        } catch (error) {
            expect(error).to.be.instanceOf(Error);
        }
    });
});
