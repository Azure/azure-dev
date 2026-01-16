// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

import { assert } from 'chai';
import * as vscode from 'vscode';
import * as sinon from 'sinon';
import { AzureYamlCodeActionProvider } from '../../../language/AzureYamlCodeActionProvider';

suite('AzureYamlCodeActionProvider', () => {
    let provider: AzureYamlCodeActionProvider;
    let sandbox: sinon.SinonSandbox;

    setup(() => {
        sandbox = sinon.createSandbox();
        provider = new AzureYamlCodeActionProvider();
    });

    teardown(() => {
        sandbox.restore();
    });

    suite('provideCodeActions', () => {
        test('provides create folder action for missing project path', async () => {
            const content = 'services:\n  api:\n    project: ./nonexistent';
            const document = await createTestDocument(content);
            const range = new vscode.Range(2, 4, 2, 28);

            const diagnostic = new vscode.Diagnostic(
                range,
                'The project path must be an existing folder or file path relative to the azure.yaml file.',
                vscode.DiagnosticSeverity.Error
            );

            const context: vscode.CodeActionContext = {
                diagnostics: [diagnostic],
                only: undefined,
                triggerKind: vscode.CodeActionTriggerKind.Automatic
            };

            const tokenSource = new vscode.CancellationTokenSource();
            const result = await provider.provideCodeActions(
                document,
                range,
                context,
                tokenSource.token
            );

            assert.ok(result);
            assert.ok(result.length > 0);

            const createFolderAction = result.find(a => a.title.includes('Create folder'));
            assert.ok(createFolderAction);
            assert.equal(createFolderAction.kind, vscode.CodeActionKind.QuickFix);
        });

        test('provides browse for folder action for missing project path', async () => {
            const content = 'services:\n  api:\n    project: ./nonexistent';
            const document = await createTestDocument(content);
            const range = new vscode.Range(2, 4, 2, 28);

            const diagnostic = new vscode.Diagnostic(
                range,
                'The project path must be an existing folder or file path relative to the azure.yaml file.',
                vscode.DiagnosticSeverity.Error
            );

            const context: vscode.CodeActionContext = {
                diagnostics: [diagnostic],
                only: undefined,
                triggerKind: vscode.CodeActionTriggerKind.Automatic
            };

            const tokenSource = new vscode.CancellationTokenSource();
            const result = await provider.provideCodeActions(
                document,
                range,
                context,
                tokenSource.token
            );

            const browseAction = result.find(a => a.title.includes('Browse'));
            assert.ok(browseAction);
            assert.equal(browseAction.kind, vscode.CodeActionKind.QuickFix);
        });

        test('provides add language actions', async () => {
            const content = 'services:\n  api:\n    project: ./api';
            const document = await createTestDocument(content);
            const range = new vscode.Range(1, 2, 1, 5);

            const diagnostic = new vscode.Diagnostic(
                range,
                'Service is missing language property',
                vscode.DiagnosticSeverity.Information
            );

            const context: vscode.CodeActionContext = {
                diagnostics: [diagnostic],
                only: undefined,
                triggerKind: vscode.CodeActionTriggerKind.Automatic
            };

            const tokenSource = new vscode.CancellationTokenSource();
            const result = await provider.provideCodeActions(
                document,
                range,
                context,
                tokenSource.token
            );

            const languageActions = result.filter(a => a.title.includes('Add language'));
            assert.ok(languageActions.length > 0);
            assert.ok(languageActions.some(a => a.title.includes('python')));
            assert.ok(languageActions.some(a => a.title.includes('js')));
        });

        test('provides add host actions', async () => {
            const content = 'services:\n  api:\n    project: ./api';
            const document = await createTestDocument(content);
            const range = new vscode.Range(1, 2, 1, 5);

            const diagnostic = new vscode.Diagnostic(
                range,
                'Service is missing host property',
                vscode.DiagnosticSeverity.Information
            );

            const context: vscode.CodeActionContext = {
                diagnostics: [diagnostic],
                only: undefined,
                triggerKind: vscode.CodeActionTriggerKind.Automatic
            };

            const tokenSource = new vscode.CancellationTokenSource();
            const result = await provider.provideCodeActions(
                document,
                range,
                context,
                tokenSource.token
            );

            const hostActions = result.filter(a => a.title.includes('Add host'));
            assert.ok(hostActions.length > 0);
            assert.ok(hostActions.some(a => a.title.includes('Container Apps')));
            assert.ok(hostActions.some(a => a.title.includes('App Service')));
        });

        test('provides add new service refactoring action', async () => {
            const content = 'services:\n  api:\n    project: ./api';
            const document = await createTestDocument(content);
            const range = new vscode.Range(0, 0, 0, 0);

            const context: vscode.CodeActionContext = {
                diagnostics: [],
                only: undefined,
                triggerKind: vscode.CodeActionTriggerKind.Automatic
            };

            const tokenSource = new vscode.CancellationTokenSource();
            const result = await provider.provideCodeActions(
                document,
                range,
                context,
                tokenSource.token
            );

            const addServiceAction = result.find(a => a.title.includes('Add new service'));
            assert.ok(addServiceAction);
            assert.equal(addServiceAction.kind, vscode.CodeActionKind.Refactor);
        });

        test('code actions have correct properties', async () => {
            const content = 'services:\n  api:\n    project: ./nonexistent';
            const document = await createTestDocument(content);
            const range = new vscode.Range(2, 4, 2, 28);

            const diagnostic = new vscode.Diagnostic(
                range,
                'The project path must be an existing folder or file path relative to the azure.yaml file.',
                vscode.DiagnosticSeverity.Error
            );

            const context: vscode.CodeActionContext = {
                diagnostics: [diagnostic],
                only: undefined,
                triggerKind: vscode.CodeActionTriggerKind.Automatic
            };

            const tokenSource = new vscode.CancellationTokenSource();
            const result = await provider.provideCodeActions(
                document,
                range,
                context,
                tokenSource.token
            );

            const createAction = result.find(a => a.title.includes('Create folder'));
            assert.ok(createAction);
            assert.ok(createAction.isPreferred); // Should be the preferred action
            assert.ok(createAction.diagnostics);
            assert.equal(createAction.diagnostics.length, 1);
        });
    });

    async function createTestDocument(content: string): Promise<vscode.TextDocument> {
        const uri = vscode.Uri.parse('untitled:test-azure.yaml');
        const doc = await vscode.workspace.openTextDocument(uri);
        const edit = new vscode.WorkspaceEdit();
        edit.insert(uri, new vscode.Position(0, 0), content);
        await vscode.workspace.applyEdit(edit);
        return doc;
    }
});
