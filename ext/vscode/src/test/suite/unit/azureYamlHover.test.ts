// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

import { assert } from 'chai';
import * as vscode from 'vscode';
import * as sinon from 'sinon';
import { AzureYamlHoverProvider } from '../../../language/AzureYamlHoverProvider';

suite('AzureYamlHoverProvider', () => {
    let provider: AzureYamlHoverProvider;
    let sandbox: sinon.SinonSandbox;

    setup(() => {
        sandbox = sinon.createSandbox();
        provider = new AzureYamlHoverProvider();
    });

    teardown(() => {
        sandbox.restore();
    });

    suite('provideHover', () => {
        test('provides hover for "host" keyword', async () => {
            const content = 'services:\n  api:\n    host: containerapp';
            const document = await createTestDocument(content);
            const position = new vscode.Position(2, 6); // On "host"
            const tokenSource = new vscode.CancellationTokenSource();

            const result = provider.provideHover(
                document,
                position,
                tokenSource.token
            );

            assert.ok(result);
            if (result && result instanceof vscode.Hover) {
                assert.ok(result.contents);
                const markdown = result.contents[0] as vscode.MarkdownString;
                assert.ok(markdown.value.includes('Azure Host'));
                assert.ok(markdown.value.includes('containerapp'));
            }
        });

        test('provides hover for "services" keyword', async () => {
            const content = 'services:\n  api:\n    project: ./api';
            const document = await createTestDocument(content);
            const position = new vscode.Position(0, 2); // On "services"
            const tokenSource = new vscode.CancellationTokenSource();

            const result = provider.provideHover(
                document,
                position,
                tokenSource.token
            );

            assert.ok(result);
            if (result && result instanceof vscode.Hover) {
                const markdown = result.contents[0] as vscode.MarkdownString;
                assert.ok(markdown.value.includes('Services'));
                assert.ok(markdown.value.includes('deployable component'));
            }
        });

        test('provides hover for "project" keyword', async () => {
            const content = 'services:\n  api:\n    project: ./api';
            const document = await createTestDocument(content);
            const position = new vscode.Position(2, 6); // On "project"
            const tokenSource = new vscode.CancellationTokenSource();

            const result = provider.provideHover(
                document,
                position,
                tokenSource.token
            );

            assert.ok(result);
            if (result && result instanceof vscode.Hover) {
                const markdown = result.contents[0] as vscode.MarkdownString;
                assert.ok(markdown.value.includes('Project Path'));
                assert.ok(markdown.value.includes('Relative path'));
            }
        });

        test('provides hover for "hooks" keyword', async () => {
            const content = 'hooks:\n  postdeploy:\n    run: echo done';
            const document = await createTestDocument(content);
            const position = new vscode.Position(0, 2); // On "hooks"
            const tokenSource = new vscode.CancellationTokenSource();

            const result = provider.provideHover(
                document,
                position,
                tokenSource.token
            );

            assert.ok(result);
            if (result && result instanceof vscode.Hover) {
                const markdown = result.contents[0] as vscode.MarkdownString;
                assert.ok(markdown.value.includes('Lifecycle Hooks'));
                assert.ok(markdown.value.includes('deployment lifecycle'));
            }
        });

        test('returns null for unknown keyword', async () => {
            const content = 'unknownkeyword: value';
            const document = await createTestDocument(content);
            const position = new vscode.Position(0, 2);
            const tokenSource = new vscode.CancellationTokenSource();

            const result = provider.provideHover(
                document,
                position,
                tokenSource.token
            );

            assert.isNull(result);
        });

        test('hover includes example code', async () => {
            const content = 'language: python';
            const document = await createTestDocument(content);
            const position = new vscode.Position(0, 2); // On "language"
            const tokenSource = new vscode.CancellationTokenSource();

            const result = provider.provideHover(
                document,
                position,
                tokenSource.token
            );

            assert.ok(result);
            if (result && result instanceof vscode.Hover) {
                const markdown = result.contents[0] as vscode.MarkdownString;
                assert.ok(markdown.value.includes('Example'));
                assert.ok(markdown.value.includes('```yaml'));
            }
        });

        test('hover includes documentation link', async () => {
            const content = 'name: myapp';
            const document = await createTestDocument(content);
            const position = new vscode.Position(0, 2); // On "name"
            const tokenSource = new vscode.CancellationTokenSource();

            const result = provider.provideHover(
                document,
                position,
                tokenSource.token
            );

            assert.ok(result);
            if (result && result instanceof vscode.Hover) {
                const markdown = result.contents[0] as vscode.MarkdownString;
                assert.ok(markdown.value.includes('View Documentation'));
                assert.ok(markdown.value.includes('learn.microsoft.com'));
            }
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
