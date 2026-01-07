// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

import * as assert from 'assert';
import * as vscode from 'vscode';
import * as sinon from 'sinon';
import { AzureYamlDiagnosticProvider } from '../../../language/AzureYamlDiagnosticProvider';
import * as azureYamlUtils from '../../../language/azureYamlUtils';

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

    test('Empty azure.yaml file shows error diagnostic', async () => {
        const document = createMockDocument('');
        const diagnostics = await diagnosticProvider.provideDiagnostics(document);

        assert.ok(diagnostics, 'Diagnostics should be returned');
        assert.strictEqual(diagnostics!.length, 1, 'Should have one diagnostic');
        assert.strictEqual(diagnostics![0].severity, vscode.DiagnosticSeverity.Error);
        assert.ok(diagnostics![0].message.includes('empty'), 'Error message should mention file is empty');
    });

    test('Whitespace-only azure.yaml file shows error diagnostic', async () => {
        const document = createMockDocument('   \n\n   \t   \n');
        const diagnostics = await diagnosticProvider.provideDiagnostics(document);

        assert.ok(diagnostics, 'Diagnostics should be returned');
        assert.strictEqual(diagnostics!.length, 1, 'Should have one diagnostic');
        assert.strictEqual(diagnostics![0].severity, vscode.DiagnosticSeverity.Error);
    });

    test('Missing name property shows error diagnostic', async () => {
        const content = `services:
  web:
    project: ./src
    host: containerapp`;

        const document = createMockDocument(content);
        const diagnostics = await diagnosticProvider.provideDiagnostics(document);

        assert.ok(diagnostics, 'Diagnostics should be returned');
        const nameDiagnostic = diagnostics!.find(d => d.message.includes('name'));
        assert.ok(nameDiagnostic, 'Should have diagnostic about missing name');
        assert.strictEqual(nameDiagnostic!.severity, vscode.DiagnosticSeverity.Error);
    });

    test('Empty name property shows error diagnostic', async () => {
        const content = `name: ""
services:
  web:
    project: ./src
    host: containerapp`;

        const document = createMockDocument(content);
        const diagnostics = await diagnosticProvider.provideDiagnostics(document);

        assert.ok(diagnostics, 'Diagnostics should be returned');
        const emptyNameDiagnostic = diagnostics!.find(d => d.message.includes('name'));
        assert.ok(emptyNameDiagnostic, 'Should have diagnostic about name');
    });

    test('Missing services section shows error diagnostic', async () => {
        const content = `name: my-app`;

        const document = createMockDocument(content);
        const diagnostics = await diagnosticProvider.provideDiagnostics(document);

        assert.ok(diagnostics, 'Diagnostics should be returned');
        const servicesDiagnostic = diagnostics!.find(d => d.message.includes('No services'));
        assert.ok(servicesDiagnostic, 'Should have diagnostic about missing services');
        assert.strictEqual(servicesDiagnostic!.severity, vscode.DiagnosticSeverity.Error);
    });

    test('Empty services section shows error diagnostic', async () => {
        const content = `name: my-app
services:`;

        const document = createMockDocument(content);
        const diagnostics = await diagnosticProvider.provideDiagnostics(document);

        assert.ok(diagnostics, 'Diagnostics should be returned');
        const servicesDiagnostic = diagnostics!.find(d => d.message.includes('No services'));
        assert.ok(servicesDiagnostic, 'Should have diagnostic about no services defined');
    });

    test('Invalid YAML syntax shows error diagnostic', async () => {
        const content = `name: my-app
services:
  web:
    project: ./src
    host: containerapp
  api
    project: ./api`;

        const document = createMockDocument(content);
        const diagnostics = await diagnosticProvider.provideDiagnostics(document);

        assert.ok(diagnostics, 'Diagnostics should be returned');
        assert.ok(diagnostics!.length > 0, 'Should have at least one diagnostic');
        const syntaxError = diagnostics!.find(d => d.severity === vscode.DiagnosticSeverity.Error);
        assert.ok(syntaxError, 'Should have error diagnostic for syntax');
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

    test('Missing language property shows information diagnostic', async () => {
        const content = `name: my-app
services:
  web:
    project: ./src
    host: containerapp`;

        const document = createMockDocument(content);
        const stub = sandbox.stub(azureYamlUtils, 'getAzureYamlProjectInformation').resolves([]);

        const diagnostics = await diagnosticProvider.provideDiagnostics(document);

        const languageDiagnostic = diagnostics?.find(d => d.message.includes('language'));
        assert.ok(languageDiagnostic, 'Should suggest adding language property');
        assert.strictEqual(languageDiagnostic!.severity, vscode.DiagnosticSeverity.Information);

        stub.restore();
    });

    test('Malformed YAML with indentation error shows helpful message', async () => {
        const content = `name: my-app
services:
web:
  project: ./src`;

        const document = createMockDocument(content);
        const diagnostics = await diagnosticProvider.provideDiagnostics(document);

        assert.ok(diagnostics, 'Diagnostics should be returned');
        assert.ok(diagnostics!.length > 0, 'Should have diagnostics');
        const error = diagnostics!.find(d => d.severity === vscode.DiagnosticSeverity.Error);
        assert.ok(error, 'Should have error diagnostic');
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

    test('Project path without ./ prefix shows information diagnostic', async () => {
        const content = `name: my-app
services:
  web:
    project: src
    host: containerapp
    language: python`;

        const document = createMockDocument(content);
        const stub = sandbox.stub(azureYamlUtils, 'getAzureYamlProjectInformation').resolves([]);

        const diagnostics = await diagnosticProvider.provideDiagnostics(document);

        const pathDiagnostic = diagnostics?.find(d => d.message.includes('should start with'));
        assert.ok(pathDiagnostic, 'Should suggest using ./ prefix');
        assert.strictEqual(pathDiagnostic!.severity, vscode.DiagnosticSeverity.Information);

        stub.restore();
    });

    test('Invalid host type shows warning diagnostic', async () => {
        const content = `name: my-app
services:
  web:
    project: ./src
    host: invalid-host-type
    language: python`;

        const document = createMockDocument(content);
        const stub = sandbox.stub(azureYamlUtils, 'getAzureYamlProjectInformation').resolves([]);

        const diagnostics = await diagnosticProvider.provideDiagnostics(document);

        const hostDiagnostic = diagnostics?.find(d => d.message.includes('Invalid host type'));
        assert.ok(hostDiagnostic, 'Should warn about invalid host type');
        assert.strictEqual(hostDiagnostic!.severity, vscode.DiagnosticSeverity.Warning);

        stub.restore();
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
