// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

import * as assert from 'assert';
import * as sinon from 'sinon';
import * as vscode from 'vscode';
import { WorkspaceAzureDevShowProvider } from '../../../services/AzureDevShowProvider';
import { IActionContext } from '@microsoft/vscode-azext-utils';
import * as azureDevCli from '../../../utils/azureDevCli';
import * as execAsync from '../../../utils/execAsync';

suite('AzureDevShowProvider Error Handling Tests', () => {
    let sandbox: sinon.SinonSandbox;
    let provider: WorkspaceAzureDevShowProvider;
    let mockContext: IActionContext;

    setup(() => {
        sandbox = sinon.createSandbox();
        provider = new WorkspaceAzureDevShowProvider();
        mockContext = {
            telemetry: {
                properties: {},
                measurements: {},
            },
            errorHandling: {
                suppressDisplay: false,
                rethrow: false,
                issueProperties: {},
            },
            valuesToMask: [],
        } as unknown as IActionContext;
    });

    teardown(() => {
        sandbox.restore();
    });

    test('Empty azure.yaml error provides user-friendly message', async () => {
        const mockAzureCli = {
            invocation: 'azd',
            spawnOptions: () => ({ cwd: '/test' })
        };

        sandbox.stub(azureDevCli, 'createAzureDevCli').resolves(mockAzureCli);
        sandbox.stub(execAsync, 'execAsync').rejects(
            new Error('ERROR: parsing project file: unable to parse azure.yaml file. File is empty.')
        );

        const configUri = vscode.Uri.file('/test/azure.yaml');

        try {
            await provider.getShowResults(mockContext, configUri);
            assert.fail('Should have thrown an error');
        } catch (error) {
            assert.ok(error instanceof Error);
            assert.ok(error.message.includes('invalid or empty'), 'Error message should be user-friendly');
            assert.ok(error.message.includes('Problems panel'), 'Error should direct user to Problems panel');
        }
    });

    test('Parse error provides user-friendly message', async () => {
        const mockAzureCli = {
            invocation: 'azd',
            spawnOptions: () => ({ cwd: '/test' })
        };

        sandbox.stub(azureDevCli, 'createAzureDevCli').resolves(mockAzureCli);
        sandbox.stub(execAsync, 'execAsync').rejects(
            new Error('ERROR: parsing project file: invalid YAML syntax')
        );

        const configUri = vscode.Uri.file('/test/azure.yaml');

        try {
            await provider.getShowResults(mockContext, configUri);
            assert.fail('Should have thrown an error');
        } catch (error) {
            assert.ok(error instanceof Error);
            assert.ok(error.message.includes('Failed to parse'), 'Error message should be user-friendly');
            assert.ok(error.message.includes('Problems panel'), 'Error should direct user to Problems panel');
        }
    });

    test('Other errors are re-thrown unchanged', async () => {
        const mockAzureCli = {
            invocation: 'azd',
            spawnOptions: () => ({ cwd: '/test' })
        };

        const originalError = new Error('Some other error');
        sandbox.stub(azureDevCli, 'createAzureDevCli').resolves(mockAzureCli);
        sandbox.stub(execAsync, 'execAsync').rejects(originalError);

        const configUri = vscode.Uri.file('/test/azure.yaml');

        try {
            await provider.getShowResults(mockContext, configUri);
            assert.fail('Should have thrown an error');
        } catch (error) {
            assert.strictEqual(error, originalError, 'Original error should be re-thrown');
        }
    });

    test('Successful parse returns results', async () => {
        const mockAzureCli = {
            invocation: 'azd',
            spawnOptions: () => ({ cwd: '/test' })
        };

        const mockResults = {
            name: 'my-app',
            services: {
                web: {
                    project: { path: './src', language: 'python' },
                    target: { resourceIds: [] }
                }
            }
        };

        sandbox.stub(azureDevCli, 'createAzureDevCli').resolves(mockAzureCli);
        sandbox.stub(execAsync, 'execAsync').resolves({ stdout: JSON.stringify(mockResults), stderr: '' });

        const configUri = vscode.Uri.file('/test/azure.yaml');
        const results = await provider.getShowResults(mockContext, configUri);

        assert.deepStrictEqual(results, mockResults);
    });
});
