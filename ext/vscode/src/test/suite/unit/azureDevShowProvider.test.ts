// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

import { expect } from 'chai';
import * as sinon from 'sinon';
import * as vscode from 'vscode';
import { WorkspaceAzureDevShowProvider } from '../../../services/AzureDevShowProvider';
import { IActionContext } from '@microsoft/vscode-azext-utils';
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
        sandbox.stub(execAsync, 'execAsync').rejects(
            new Error('ERROR: parsing project file: unable to parse azure.yaml file. File is empty.')
        );

        const configUri = vscode.Uri.file('/test/azure.yaml');

        try {
            await provider.getShowResults(mockContext, configUri);
            expect.fail('Should have thrown an error');
        } catch (error) {
            expect(error).to.be.instanceOf(Error);
            expect((error as Error).message, 'Error message should be user-friendly').to.include('invalid or empty');
            expect((error as Error).message, 'Error should direct user to Problems panel').to.include('Problems panel');
        }
    });

    test('Parse error provides user-friendly message', async () => {
        sandbox.stub(execAsync, 'execAsync').rejects(
            new Error('ERROR: parsing project file: invalid YAML syntax')
        );

        const configUri = vscode.Uri.file('/test/azure.yaml');

        try {
            await provider.getShowResults(mockContext, configUri);
            expect.fail('Should have thrown an error');
        } catch (error) {
            expect(error).to.be.instanceOf(Error);
            expect((error as Error).message, 'Error message should be user-friendly').to.include('Failed to parse');
            expect((error as Error).message, 'Error should direct user to Problems panel').to.include('Problems panel');
        }
    });

    test('Other errors are re-thrown unchanged', async () => {
        const originalError = new Error('Some other error');
        sandbox.stub(execAsync, 'execAsync').rejects(originalError);

        const configUri = vscode.Uri.file('/test/azure.yaml');

        try {
            await provider.getShowResults(mockContext, configUri);
            expect.fail('Should have thrown an error');
        } catch (error) {
            expect(error).to.equal(originalError, 'Original error should be re-thrown');
        }
    });

    test('Successful parse returns results', async () => {
        const mockResults = {
            name: 'my-app',
            services: {
                web: {
                    project: { path: './src', language: 'python' },
                    target: { resourceIds: [] }
                }
            }
        };

        sandbox.stub(execAsync, 'execAsync').resolves({ stdout: JSON.stringify(mockResults), stderr: '' });

        const configUri = vscode.Uri.file('/test/azure.yaml');
        const results = await provider.getShowResults(mockContext, configUri);

        expect(results).to.deep.equal(mockResults);
    });
});
