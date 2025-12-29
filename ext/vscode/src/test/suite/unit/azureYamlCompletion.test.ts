// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

import { assert } from 'chai';
import * as vscode from 'vscode';
import * as sinon from 'sinon';
import { AzureYamlCompletionProvider } from '../../../language/AzureYamlCompletionProvider';

suite('AzureYamlCompletionProvider', () => {
    let provider: AzureYamlCompletionProvider;
    let document: vscode.TextDocument;
    let sandbox: sinon.SinonSandbox;

    setup(() => {
        sandbox = sinon.createSandbox();
        provider = new AzureYamlCompletionProvider();
    });

    teardown(() => {
        sandbox.restore();
    });

    suite('provideCompletionItems', () => {
        test('provides host type completions after "host:"', async () => {
            const content = 'services:\n  api:\n    host:';
            document = await createTestDocument(content);
            const position = new vscode.Position(2, 9); // After "host:"
            const tokenSource = new vscode.CancellationTokenSource();

            const result = provider.provideCompletionItems(
                document,
                position,
                tokenSource.token,
                { triggerKind: vscode.CompletionTriggerKind.Invoke, triggerCharacter: ':' }
            );

            assert.ok(result);
            const items = Array.isArray(result) ? result : (result as vscode.CompletionList)?.items || [];
            assert.ok(items.length > 0);

            const hostLabels = items.map(i => i.label);
            assert.include(hostLabels, 'containerapp');
            assert.include(hostLabels, 'appservice');
            assert.include(hostLabels, 'function');
        });

        test('provides hook type completions in hooks section', async () => {
            const content = 'services:\n  api:\n    hooks:\n      ';
            document = await createTestDocument(content);
            const position = new vscode.Position(3, 6); // In hooks section
            const tokenSource = new vscode.CancellationTokenSource();

            const result = provider.provideCompletionItems(
                document,
                position,
                tokenSource.token,
                { triggerKind: vscode.CompletionTriggerKind.Invoke, triggerCharacter: '\n' }
            );

            assert.ok(result);
            const items = Array.isArray(result) ? result : (result as vscode.CompletionList)?.items || [];

            const hookLabels = items.map(i => i.label);
            assert.include(hookLabels, 'prerestore');
            assert.include(hookLabels, 'postprovision');
            assert.include(hookLabels, 'predeploy');
        });

        test('provides service property completions', async () => {
            const content = 'services:\n  api:\n    ';
            document = await createTestDocument(content);
            const position = new vscode.Position(2, 4); // Under service
            const tokenSource = new vscode.CancellationTokenSource();

            const result = provider.provideCompletionItems(
                document,
                position,
                tokenSource.token,
                { triggerKind: vscode.CompletionTriggerKind.Invoke, triggerCharacter: '\n' }
            );

            assert.ok(result);
            const items = Array.isArray(result) ? result : (result as vscode.CompletionList)?.items || [];

            const propertyLabels = items.map(i => i.label);
            assert.include(propertyLabels, 'project');
            assert.include(propertyLabels, 'language');
            assert.include(propertyLabels, 'host');
        });

        test('provides top-level property completions', async () => {
            const content = '';
            document = await createTestDocument(content);
            const position = new vscode.Position(0, 0); // At root
            const tokenSource = new vscode.CancellationTokenSource();

            const result = provider.provideCompletionItems(
                document,
                position,
                tokenSource.token,
                { triggerKind: vscode.CompletionTriggerKind.Invoke, triggerCharacter: '\n' }
            );

            assert.ok(result);
            const items = Array.isArray(result) ? result : (result as vscode.CompletionList)?.items || [];

            const propertyLabels = items.map(i => i.label);
            assert.include(propertyLabels, 'name');
            assert.include(propertyLabels, 'services');
        });

        test('completion items have correct kind', async () => {
            const content = 'services:\n  api:\n    host:';
            document = await createTestDocument(content);
            const position = new vscode.Position(2, 9);
            const tokenSource = new vscode.CancellationTokenSource();

            const result = provider.provideCompletionItems(
                document,
                position,
                tokenSource.token,
                { triggerKind: vscode.CompletionTriggerKind.Invoke, triggerCharacter: ':' }
            );

            const items = Array.isArray(result) ? result : (result as vscode.CompletionList)?.items || [];
            const firstItem = items[0];

            assert.equal(firstItem.kind, vscode.CompletionItemKind.Value);
        });

        test('completion items have documentation', async () => {
            const content = 'services:\n  api:\n    host:';
            document = await createTestDocument(content);
            const position = new vscode.Position(2, 9);
            const tokenSource = new vscode.CancellationTokenSource();

            const result = provider.provideCompletionItems(
                document,
                position,
                tokenSource.token,
                { triggerKind: vscode.CompletionTriggerKind.Invoke, triggerCharacter: ':' }
            );

            const items = Array.isArray(result) ? result : (result as vscode.CompletionList)?.items || [];
            const firstItem = items[0];

            assert.ok(firstItem.documentation);
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
