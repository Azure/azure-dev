// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

import { expect } from 'chai';
import * as vscode from 'vscode';
import * as sinon from 'sinon';
import { provision } from '../../../commands/provision';
import { IActionContext } from '@microsoft/vscode-azext-utils';
import { AzureDevCliApplication } from '../../../views/workspace/AzureDevCliApplication';

suite('provision command', () => {
    let sandbox: sinon.SinonSandbox;
    let mockContext: IActionContext;

    setup(() => {
        sandbox = sinon.createSandbox();
        
        mockContext = {
            errorHandling: {
                suppressReportIssue: false
            },
            telemetry: {
                properties: {}
            }
        } as unknown as IActionContext;
    });

    teardown(() => {
        sandbox.restore();
    });

    test('throws error when selectedFile has undefined fsPath (virtual file system)', async () => {
        // Create a URI with a scheme that doesn't support fsPath
        const virtualUri = vscode.Uri.parse('untitled:Untitled-1');
        
        // Mock the URI to ensure fsPath is undefined
        const mockUri = {
            scheme: 'virtual',
            fsPath: undefined as unknown as string
        } as vscode.Uri;

        try {
            await provision(mockContext, mockUri);
            expect.fail('Should have thrown an error');
        } catch (error) {
            expect(error).to.be.instanceOf(Error);
            const errMessage = (error as Error).message;
            expect(errMessage).to.include('Unable to determine working folder');
            expect(errMessage).to.include('virtual');
            expect(errMessage).to.include('vscode.Uri');
            expect(errMessage).to.include('virtual file systems');
        }

        expect(mockContext.errorHandling.suppressReportIssue).to.equal(true);
    });

    test('throws error when TreeViewModel has undefined fsPath', async () => {
        const mockTreeViewModel = {
            unwrap: () => ({
                context: {
                    configurationFile: {
                        scheme: 'virtual',
                        fsPath: undefined as unknown as string
                    } as vscode.Uri
                }
            })
        };

        // Stub the isTreeViewModel function
        const isTreeViewModelStub = sandbox.stub().returns(true);
        const isAzureDevCliModelStub = sandbox.stub().returns(false);

        try {
            await provision(mockContext, mockTreeViewModel as any);
            expect.fail('Should have thrown an error');
        } catch (error) {
            expect(error).to.be.instanceOf(Error);
            const errMessage = (error as Error).message;
            expect(errMessage).to.include('Unable to determine working folder');
            expect(errMessage).to.include('virtual');
        }

        expect(mockContext.errorHandling.suppressReportIssue).to.equal(true);
    });

    test('throws error when AzureDevCliModel has undefined fsPath', async () => {
        const mockAzureDevCliModel = {
            context: {
                configurationFile: {
                    scheme: 'virtual',
                    fsPath: undefined as unknown as string
                } as vscode.Uri
            }
        };

        // Stub the type check functions
        const isTreeViewModelStub = sandbox.stub().returns(false);
        const isAzureDevCliModelStub = sandbox.stub().returns(true);

        try {
            await provision(mockContext, mockAzureDevCliModel as any);
            expect.fail('Should have thrown an error');
        } catch (error) {
            expect(error).to.be.instanceOf(Error);
            const errMessage = (error as Error).message;
            expect(errMessage).to.include('Unable to determine working folder');
            expect(errMessage).to.include('virtual');
        }

        expect(mockContext.errorHandling.suppressReportIssue).to.equal(true);
    });
});
