// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

import { expect } from 'chai';
import * as vscode from 'vscode';
import * as sinon from 'sinon';
import { provision } from '../../../commands/provision';
import { IActionContext } from '@microsoft/vscode-azext-utils';

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
        // Mock the URI to ensure fsPath is undefined - simulates virtual file system
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

        expect(mockContext.errorHandling.suppressReportIssue).to.equal(true, 'Should suppress automatic issue reporting for user errors');
    });
});
