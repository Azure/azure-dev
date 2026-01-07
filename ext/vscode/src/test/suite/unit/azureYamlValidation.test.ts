// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

import * as assert from 'assert';
import * as vscode from 'vscode';
import * as sinon from 'sinon';
import { AzureYamlDiagnosticProvider } from '../../../language/AzureYamlDiagnosticProvider';
import * as azureYamlUtils from '../../../language/azureYamlUtils';

/**
 * Tests for AzureYamlDiagnosticProvider
 *
 * Note: Schema validation (required properties, valid values, YAML syntax, etc.) is handled by
 * the RedHat YAML extension using the azure.yaml JSON schema. This provider only
 * handles validation that requires runtime checks, such as verifying that project
 * paths exist on disk.
 */
suite('Azure YAML Validation Tests', () => {
    let diagnosticProvider: AzureYamlDiagnosticProvider;
    let sandbox: sinon.SinonSandbox;

    setup(() => {
        sandbox = sinon.createSandbox();
        const selector = { language: 'yaml', scheme: 'file', pattern: '**/azure.{yml,yaml}' };
        diagnosticProvider = new AzureYamlDiagnosticProvider(selector);
    });

    teardown(() => {
        sandbox.restore();
        diagnosticProvider.dispose();
    });

    test('Valid minimal azure.yaml shows no errors', async () => {
        const content = `name: my-app
services:
  web:
    project: ./src
    host: containerapp
    language: python`;

        const document = createMockDocument(content);
        const stub = sandbox.stub(azureYamlUtils, 'getAzureYamlProjectInformation').resolves([]);

        const diagnostics = await diagnosticProvider.provideDiagnostics(document);

        const errors = diagnostics?.filter(d => d.severity === vscode.DiagnosticSeverity.Error) || [];
        assert.strictEqual(errors.length, 0, 'Valid YAML should have no errors');

        stub.restore();
    });

    test('Valid complex azure.yaml with multiple services shows no errors', async () => {
        const content = `name: my-complex-app
services:
  web:
    project: ./src/web
    host: containerapp
    language: python
  api:
    project: ./src/api
    host: containerapp
    language: nodejs
  functions:
    project: ./src/functions
    host: function
    language: csharp`;

        const document = createMockDocument(content);
        const stub = sandbox.stub(azureYamlUtils, 'getAzureYamlProjectInformation').resolves([]);

        const diagnostics = await diagnosticProvider.provideDiagnostics(document);

        const errors = diagnostics?.filter(d => d.severity === vscode.DiagnosticSeverity.Error) || [];
        assert.strictEqual(errors.length, 0, 'Valid complex YAML should have no errors');

        stub.restore();
    });

    test('Non-existent project path shows error diagnostic', async () => {
        const content = `name: my-app
services:
  web:
    project: ./nonexistent
    host: containerapp
    language: python`;

        const document = createMockDocument(content);
        // Mock to return the project info with a non-existent path
        const stub = sandbox.stub(azureYamlUtils, 'getAzureYamlProjectInformation').resolves([
            {
                azureYamlUri: vscode.Uri.file('/test/azure.yaml'),
                serviceName: 'web',
                projectValue: './nonexistent',
                projectUri: vscode.Uri.file('/test/nonexistent'),
                projectValueNodeRange: new vscode.Range(4, 13, 4, 27)
            }
        ]);

        const diagnostics = await diagnosticProvider.provideDiagnostics(document);

        const pathDiagnostic = diagnostics?.find(d => d.message.includes('project path'));
        assert.ok(pathDiagnostic, 'Should have diagnostic about missing project path');
        assert.strictEqual(pathDiagnostic!.severity, vscode.DiagnosticSeverity.Error);

        stub.restore();
    });

    test('Handles empty file gracefully', async () => {
        const document = createMockDocument('');

        // Should not throw
        const diagnostics = await diagnosticProvider.provideDiagnostics(document);

        // May return undefined or empty array for empty file
        assert.ok(diagnostics === undefined || Array.isArray(diagnostics));
    });

    test('Handles malformed YAML gracefully', async () => {
        const content = `name: my-app
services:
  web:
    project: ./src
    host: containerapp
  api
    project: ./api`;

        const document = createMockDocument(content);

        // Should not throw
        const diagnostics = await diagnosticProvider.provideDiagnostics(document);

        // May return undefined or empty array for malformed YAML
        assert.ok(diagnostics === undefined || Array.isArray(diagnostics));
    });

    function createMockDocument(content: string): vscode.TextDocument {
        const uri = vscode.Uri.file('/test/azure.yaml');
        return {
            uri,
            fileName: uri.fsPath,
            languageId: 'yaml',
            version: 1,
            getText: () => content,
            lineCount: content.split('\n').length,
            positionAt: (offset: number) => {
                const lines = content.substring(0, offset).split('\n');
                const line = lines.length - 1;
                const character = lines[lines.length - 1].length;
                return new vscode.Position(line, character);
            },
            offsetAt: (position: vscode.Position) => {
                const lines = content.split('\n');
                let offset = 0;
                for (let i = 0; i < position.line && i < lines.length; i++) {
                    offset += lines[i].length + 1; // +1 for newline
                }
                return offset + position.character;
            },
            lineAt: (line: number | vscode.Position) => {
                const lineNumber = typeof line === 'number' ? line : line.line;
                const lines = content.split('\n');
                const text = lines[lineNumber] || '';
                return {
                    lineNumber,
                    text,
                    range: new vscode.Range(lineNumber, 0, lineNumber, text.length),
                    rangeIncludingLineBreak: new vscode.Range(lineNumber, 0, lineNumber + 1, 0),
                    firstNonWhitespaceCharacterIndex: text.search(/\S/),
                    isEmptyOrWhitespace: text.trim().length === 0
                };
            },
            getWordRangeAtPosition: () => undefined,
            validateRange: (range: vscode.Range) => range,
            validatePosition: (position: vscode.Position) => position,
            save: () => Promise.resolve(true),
            eol: vscode.EndOfLine.LF,
            isDirty: false,
            isClosed: false,
            isUntitled: false
        } as vscode.TextDocument;
    }
});
