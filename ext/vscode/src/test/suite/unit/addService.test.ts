// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

import * as assert from 'assert';
import * as vscode from 'vscode';
import * as sinon from 'sinon';
import { addService } from '../../../commands/addService';
import { IActionContext } from '@microsoft/vscode-azext-utils';
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
        mockContext = {} as IActionContext;
        showInputBoxStub = sandbox.stub(vscode.window, 'showInputBox');
        showQuickPickStub = sandbox.stub(vscode.window, 'showQuickPick');
        showErrorMessageStub = sandbox.stub(vscode.window, 'showErrorMessage');
        showInformationMessageStub = sandbox.stub(vscode.window, 'showInformationMessage');
        openTextDocumentStub = sandbox.stub(vscode.workspace, 'openTextDocument');
        applyEditStub = sandbox.stub(vscode.workspace, 'applyEdit');
    });

    teardown(() => {
        sandbox.restore();
    });

    test('returns early when user cancels service name input', async () => {
        const mockNode = {
            context: {
                configurationFile: vscode.Uri.file('/test/azure.yaml')
            }
        } as AzureDevCliModel;

        showInputBoxStub.resolves(undefined);

        await addService(mockContext, mockNode);

        assert.strictEqual(showQuickPickStub.called, false, 'showQuickPick should not be called if service name is cancelled');
    });

    test('validates service name input correctly', async () => {
        const mockNode = {
            context: {
                configurationFile: vscode.Uri.file('/test/azure.yaml')
            }
        } as AzureDevCliModel;

        showInputBoxStub.resolves('my-service');

        // Call the function to trigger showInputBox
        await addService(mockContext, mockNode);

        // Get the validator function
        assert.ok(showInputBoxStub.called, 'showInputBox should be called');
        const inputBoxOptions = showInputBoxStub.firstCall?.args[0] as vscode.InputBoxOptions;
        const validator = inputBoxOptions?.validateInput;

        assert.ok(validator, 'Validator should be provided');
        
        if (validator) {
            // Valid names
            assert.strictEqual(validator('my-service'), undefined);
            assert.strictEqual(validator('my_service'), undefined);
            assert.strictEqual(validator('myService123'), undefined);

            // Invalid names
            assert.ok(validator(''), 'Empty string should be invalid');
            assert.ok(validator('my service'), 'Space should be invalid');
            assert.ok(validator('my@service'), 'Special character should be invalid');
        }
    });

    test('returns early when user cancels language selection', async () => {
        const mockNode = {
            context: {
                configurationFile: vscode.Uri.file('/test/azure.yaml')
            }
        } as AzureDevCliModel;

        showInputBoxStub.resolves('my-service');
        showQuickPickStub.onFirstCall().resolves(undefined);

        await addService(mockContext, mockNode);

        assert.strictEqual(showQuickPickStub.callCount, 1, 'Should only call showQuickPick once for language');
    });

    test('returns early when user cancels host selection', async () => {
        const mockNode = {
            context: {
                configurationFile: vscode.Uri.file('/test/azure.yaml')
            }
        } as AzureDevCliModel;

        showInputBoxStub.resolves('my-service');
        showQuickPickStub.onFirstCall().resolves('python');
        showQuickPickStub.onSecondCall().resolves(undefined);

        await addService(mockContext, mockNode);

        assert.strictEqual(showQuickPickStub.callCount, 2, 'Should call showQuickPick twice (language and host)');
        assert.strictEqual(openTextDocumentStub.called, false, 'Should not open document if host is cancelled');
    });

    test('adds service with correct YAML structure when all inputs provided', async () => {
        const mockNode = {
            context: {
                configurationFile: vscode.Uri.file('/test/azure.yaml')
            }
        } as AzureDevCliModel;

        const mockDocument = {
            getText: () => `name: test-app\nservices:\n  web:\n    project: ./web\n    language: ts\n    host: containerapp\n`,
            positionAt: (offset: number) => new vscode.Position(0, 0)
        };

        showInputBoxStub.resolves('api');
        showQuickPickStub.onFirstCall().resolves('python');
        showQuickPickStub.onSecondCall().resolves({ label: 'containerapp', description: 'Azure Container Apps' });
        openTextDocumentStub.resolves(mockDocument);
        applyEditStub.resolves(true);

        await addService(mockContext, mockNode);

        assert.ok(applyEditStub.called, 'applyEdit should be called');
        assert.ok(showInformationMessageStub.called, 'Success message should be shown');
        
        const successMessage = showInformationMessageStub.firstCall.args[0] as string;
        assert.ok(successMessage.includes('api'), 'Success message should include service name');
    });

    test('shows error when services section not found in azure.yaml', async () => {
        const mockNode = {
            context: {
                configurationFile: vscode.Uri.file('/test/azure.yaml')
            }
        } as AzureDevCliModel;

        const mockDocument = {
            getText: () => `name: test-app\n`,
            positionAt: (offset: number) => new vscode.Position(0, 0)
        };

        showInputBoxStub.resolves('api');
        showQuickPickStub.onFirstCall().resolves('python');
        showQuickPickStub.onSecondCall().resolves({ label: 'containerapp', description: 'Azure Container Apps' });
        openTextDocumentStub.resolves(mockDocument);

        await addService(mockContext, mockNode);

        assert.ok(showErrorMessageStub.called, 'Error message should be shown');
        const errorMessage = showErrorMessageStub.firstCall.args[0] as string;
        assert.ok(errorMessage.includes('No services section'), 'Error should mention missing services section');
    });

    test('searches for azure.yaml when node has no configuration file', async () => {
        const findFilesStub = sandbox.stub(vscode.workspace, 'findFiles');
        sandbox.stub(vscode.workspace, 'workspaceFolders').get(() => [
            { uri: vscode.Uri.file('/test'), name: 'test', index: 0 }
        ]);

        findFilesStub.resolves([vscode.Uri.file('/test/azure.yaml')]);

        const mockDocument = {
            getText: () => `name: test-app\nservices:\n  web:\n    project: ./web\n`,
            positionAt: (offset: number) => new vscode.Position(0, 0)
        };

        showInputBoxStub.resolves('api');
        showQuickPickStub.onFirstCall().resolves('python');
        showQuickPickStub.onSecondCall().resolves({ label: 'function', description: 'Azure Functions' });
        openTextDocumentStub.resolves(mockDocument);
        applyEditStub.resolves(true);

        await addService(mockContext);

        assert.ok(findFilesStub.called, 'Should search for azure.yaml files');
        assert.ok(openTextDocumentStub.called, 'Should open the found azure.yaml file');
    });

    test('shows error when no workspace folder is open', async () => {
        sandbox.stub(vscode.workspace, 'workspaceFolders').get(() => undefined);

        await addService(mockContext);

        assert.ok(showErrorMessageStub.called, 'Error message should be shown');
        const errorMessage = showErrorMessageStub.firstCall.args[0] as string;
        assert.ok(errorMessage.includes('No workspace folder'), 'Error should mention no workspace folder');
    });

    test('shows error when no azure.yaml found in workspace', async () => {
        const findFilesStub = sandbox.stub(vscode.workspace, 'findFiles');
        sandbox.stub(vscode.workspace, 'workspaceFolders').get(() => [
            { uri: vscode.Uri.file('/test'), name: 'test', index: 0 }
        ]);

        findFilesStub.resolves([]);

        await addService(mockContext);

        assert.ok(showErrorMessageStub.called, 'Error message should be shown');
        const errorMessage = showErrorMessageStub.firstCall.args[0] as string;
        assert.ok(errorMessage.includes('No azure.yaml file found'), 'Error should mention no azure.yaml found');
    });

    test('generates correct service snippet with different host types', async () => {
        const mockNode = {
            context: {
                configurationFile: vscode.Uri.file('/test/azure.yaml')
            }
        } as AzureDevCliModel;

        const mockDocument = {
            getText: () => `name: test-app\nservices:\n  web:\n    project: ./web\n`,
            positionAt: (offset: number) => new vscode.Position(0, 0)
        };

        // Test with different host types
        const hostTypes = [
            { label: 'containerapp', description: 'Azure Container Apps' },
            { label: 'appservice', description: 'Azure App Service' },
            { label: 'function', description: 'Azure Functions' }
        ];

        for (const host of hostTypes) {
            showInputBoxStub.resolves('api');
            showQuickPickStub.onFirstCall().resolves('python');
            showQuickPickStub.onSecondCall().resolves(host);
            openTextDocumentStub.resolves(mockDocument);
            applyEditStub.resolves(true);

            await addService(mockContext, mockNode);

            assert.ok(applyEditStub.called, `applyEdit should be called for host ${host.label}`);
            
            // Reset stubs for next iteration
            applyEditStub.resetHistory();
            showQuickPickStub.resetHistory();
        }
    });
});
