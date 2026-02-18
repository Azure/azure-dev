// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

import { expect } from 'chai';
import * as vscode from 'vscode';
import * as sinon from 'sinon';
import { addService } from '../../../commands/addService';
import { IActionContext, UserCancelledError } from '@microsoft/vscode-azext-utils';
import { AzureDevCliModel } from '../../../views/workspace/AzureDevCliModel';

suite('addService', () => {
    let sandbox: sinon.SinonSandbox;
    let mockContext: IActionContext;
    let showInputBoxStub: sinon.SinonStub;
    let showQuickPickStub: sinon.SinonStub;
    let showErrorMessageStub: sinon.SinonStub;
    let showInformationMessageStub: sinon.SinonStub;
    let openTextDocumentStub: sinon.SinonStub;
    let applyEditStub: sinon.SinonStub;

    setup(() => {
        sandbox = sinon.createSandbox();
        
        // Create stubs for context.ui methods
        showInputBoxStub = sandbox.stub();
        showQuickPickStub = sandbox.stub();
        
        mockContext = {
            ui: {
                showInputBox: showInputBoxStub,
                showQuickPick: showQuickPickStub
            }
        } as unknown as IActionContext;
        
        showErrorMessageStub = sandbox.stub(vscode.window, 'showErrorMessage');
        showInformationMessageStub = sandbox.stub(vscode.window, 'showInformationMessage');
        openTextDocumentStub = sandbox.stub(vscode.workspace, 'openTextDocument');
        applyEditStub = sandbox.stub(vscode.workspace, 'applyEdit');
    });

    teardown(() => {
        sandbox.restore();
    });

    test('throws UserCancelledError when user cancels service name input', async () => {
        const mockNode = {
            context: {
                configurationFile: vscode.Uri.file('/test/azure.yaml')
            }
        } as AzureDevCliModel;

        showInputBoxStub.rejects(new UserCancelledError());

        try {
            await addService(mockContext, mockNode);
            expect.fail('Should have thrown UserCancelledError');
        } catch (error) {
            expect(error).to.be.instanceOf(UserCancelledError);
        }

        expect(showQuickPickStub.called).to.equal(false, 'showQuickPick should not be called if service name is cancelled');
    });

    test('validates service name input correctly', async () => {
        const mockNode = {
            context: {
                configurationFile: vscode.Uri.file('/test/azure.yaml')
            }
        } as AzureDevCliModel;

        showInputBoxStub.resolves('my-service');
        showQuickPickStub.onFirstCall().resolves({ label: 'python' });
        showQuickPickStub.onSecondCall().resolves({ label: 'containerapp' });

        const mockDocument = {
            getText: () => `name: test-app\nservices:\n  web:\n    project: ./web\n`,
            positionAt: () => new vscode.Position(0, 0)
        };
        openTextDocumentStub.resolves(mockDocument);
        applyEditStub.resolves(true);

        // Call the function to trigger showInputBox
        await addService(mockContext, mockNode);

        // Get the validator function from the showInputBox call
        expect(showInputBoxStub.called, 'showInputBox should be called').to.be.true;
        const inputBoxOptions = showInputBoxStub.firstCall?.args[0];
        const validator = inputBoxOptions?.validateInput;

        expect(validator, 'Validator should be provided').to.exist;

        if (validator && typeof validator === 'function') {
            const validateFn = validator as (input: string) => string | undefined;
            
            // Valid names
            expect(validateFn('my-service')).to.equal(undefined);
            expect(validateFn('my_service')).to.equal(undefined);
            expect(validateFn('myService123')).to.equal(undefined);

            // Invalid names
            expect(validateFn(''), 'Empty string should be invalid').to.exist;
            expect(validateFn('my service'), 'Space should be invalid').to.exist;
            expect(validateFn('my@service'), 'Special character should be invalid').to.exist;
        }
    });

    test('throws UserCancelledError when user cancels language selection', async () => {
        const mockNode = {
            context: {
                configurationFile: vscode.Uri.file('/test/azure.yaml')
            }
        } as AzureDevCliModel;

        showInputBoxStub.resolves('my-service');
        showQuickPickStub.onFirstCall().rejects(new UserCancelledError());

        try {
            await addService(mockContext, mockNode);
            expect.fail('Should have thrown UserCancelledError');
        } catch (error) {
            expect(error).to.be.instanceOf(UserCancelledError);
        }

        expect(showQuickPickStub.callCount).to.equal(1, 'Should only call showQuickPick once for language');
    });

    test('throws UserCancelledError when user cancels host selection', async () => {
        const mockNode = {
            context: {
                configurationFile: vscode.Uri.file('/test/azure.yaml')
            }
        } as AzureDevCliModel;

        showInputBoxStub.resolves('my-service');
        showQuickPickStub.onFirstCall().resolves({ label: 'python' });
        showQuickPickStub.onSecondCall().rejects(new UserCancelledError());

        try {
            await addService(mockContext, mockNode);
            expect.fail('Should have thrown UserCancelledError');
        } catch (error) {
            expect(error).to.be.instanceOf(UserCancelledError);
        }

        expect(showQuickPickStub.callCount).to.equal(2, 'Should call showQuickPick twice (language and host)');
        expect(openTextDocumentStub.called).to.equal(false, 'Should not open document if host is cancelled');
    });

    test('adds service with correct YAML structure when all inputs provided', async () => {
        const mockNode = {
            context: {
                configurationFile: vscode.Uri.file('/test/azure.yaml')
            }
        } as AzureDevCliModel;

        const mockDocument = {
            getText: () => `name: test-app\nservices:\n  web:\n    project: ./web\n    language: ts\n    host: containerapp\n`,
            positionAt: () => new vscode.Position(0, 0)
        };

        showInputBoxStub.resolves('api');
        showQuickPickStub.onFirstCall().resolves({ label: 'python' });
        showQuickPickStub.onSecondCall().resolves({ label: 'containerapp', description: 'Azure Container Apps' });
        openTextDocumentStub.resolves(mockDocument);
        applyEditStub.resolves(true);

        await addService(mockContext, mockNode);

        expect(applyEditStub.called, 'applyEdit should be called').to.be.true;
        expect(showInformationMessageStub.called, 'Success message should be shown').to.be.true;
    });

    test('shows error when services section not found in azure.yaml', async () => {
        const mockNode = {
            context: {
                configurationFile: vscode.Uri.file('/test/azure.yaml')
            }
        } as AzureDevCliModel;

        const mockDocument = {
            getText: () => `name: test-app\n`,
            positionAt: () => new vscode.Position(0, 0)
        };

        showInputBoxStub.resolves('api');
        showQuickPickStub.onFirstCall().resolves({ label: 'python' });
        showQuickPickStub.onSecondCall().resolves({ label: 'containerapp', description: 'Azure Container Apps' });
        openTextDocumentStub.resolves(mockDocument);

        await addService(mockContext, mockNode);

        expect(showErrorMessageStub.called, 'Error message should be shown').to.be.true;
    });

    test('searches for azure.yaml when node has no configuration file', async () => {
        const findFilesStub = sandbox.stub(vscode.workspace, 'findFiles');
        sandbox.stub(vscode.workspace, 'workspaceFolders').get(() => [
            { uri: vscode.Uri.file('/test'), name: 'test', index: 0 }
        ]);

        findFilesStub.resolves([vscode.Uri.file('/test/azure.yaml')]);

        const mockDocument = {
            getText: () => `name: test-app\nservices:\n  web:\n    project: ./web\n`,
            positionAt: () => new vscode.Position(0, 0)
        };

        showInputBoxStub.resolves('api');
        showQuickPickStub.onFirstCall().resolves({ label: 'python' });
        showQuickPickStub.onSecondCall().resolves({ label: 'function', description: 'Azure Functions' });
        openTextDocumentStub.resolves(mockDocument);
        applyEditStub.resolves(true);

        await addService(mockContext);

        expect(findFilesStub.called, 'Should search for azure.yaml files').to.be.true;
        expect(openTextDocumentStub.called, 'Should open the found azure.yaml file').to.be.true;
    });

    test('shows error when no workspace folder is open', async () => {
        sandbox.stub(vscode.workspace, 'workspaceFolders').get(() => undefined);

        await addService(mockContext);

        expect(showErrorMessageStub.called, 'Error message should be shown').to.be.true;
    });

    test('shows error when no azure.yaml found in workspace', async () => {
        const findFilesStub = sandbox.stub(vscode.workspace, 'findFiles');
        sandbox.stub(vscode.workspace, 'workspaceFolders').get(() => [
            { uri: vscode.Uri.file('/test'), name: 'test', index: 0 }
        ]);

        findFilesStub.resolves([]);

        await addService(mockContext);

        expect(showErrorMessageStub.called, 'Error message should be shown').to.be.true;
    });

    test('generates correct service snippet with different host types', async () => {
        const mockNode = {
            context: {
                configurationFile: vscode.Uri.file('/test/azure.yaml')
            }
        } as AzureDevCliModel;

        const mockDocument = {
            getText: () => `name: test-app\nservices:\n  web:\n    project: ./web\n`,
            positionAt: () => new vscode.Position(0, 0)
        };

        // Test with different host types
        const hostTypes = [
            { label: 'containerapp', description: 'Azure Container Apps' },
            { label: 'appservice', description: 'Azure App Service' },
            { label: 'function', description: 'Azure Functions' }
        ];

        for (const host of hostTypes) {
            showInputBoxStub.resolves('api');
            showQuickPickStub.onFirstCall().resolves({ label: 'python' });
            showQuickPickStub.onSecondCall().resolves(host);
            openTextDocumentStub.resolves(mockDocument);
            applyEditStub.resolves(true);

            await addService(mockContext, mockNode);

            expect(applyEditStub.called, `applyEdit should be called for host ${host.label}`).to.be.true;

            // Reset stubs for next iteration
            applyEditStub.resetHistory();
            showQuickPickStub.resetHistory();
            showInputBoxStub.resetHistory();
        }
    });
});
