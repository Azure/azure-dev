// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

import { assert } from 'chai';
import * as vscode from 'vscode';
import * as sinon from 'sinon';
import { AzExtFsExtra } from '@microsoft/vscode-azext-utils';
import { AzureYamlDiagnosticProvider } from '../../../language/AzureYamlDiagnosticProvider';

suite('AzureYamlDiagnosticProvider - Enhanced Validation', () => {
    let provider: AzureYamlDiagnosticProvider;
    let sandbox: sinon.SinonSandbox;
    const selector: vscode.DocumentSelector = {
        language: 'yaml',
        scheme: 'file',
        pattern: '**/azure.{yml,yaml}'
    };

    setup(() => {
        sandbox = sinon.createSandbox();
        provider = new AzureYamlDiagnosticProvider(selector);
    });

    teardown(() => {
        sandbox.restore();
        provider.dispose();
    });

    suite('validateYamlStructure', () => {
        test('warns about missing name property', async () => {
            const content = 'services:\n  api:\n    project: ./api';
            const document = await createTestDocument(content, 'azure.yaml');

            const diagnostics = await provider.provideDiagnostics(document);

            assert.ok(diagnostics);
            const nameDiagnostic = diagnostics.find(d => d.message.includes('name'));
            assert.ok(nameDiagnostic);
            assert.equal(nameDiagnostic.severity, vscode.DiagnosticSeverity.Warning);
        });

        test('provides info for missing language property', async () => {
            const content = 'name: myapp\nservices:\n  api:\n    project: ./api\n    host: containerapp';
            const document = await createTestDocument(content, 'azure.yaml');

            const diagnostics = await provider.provideDiagnostics(document);

            assert.ok(diagnostics);
            const languageDiagnostic = diagnostics.find(d => d.message.includes('language'));
            assert.ok(languageDiagnostic);
            assert.equal(languageDiagnostic.severity, vscode.DiagnosticSeverity.Information);
        });

        test('provides info for missing host property', async () => {
            const content = 'name: myapp\nservices:\n  api:\n    project: ./api\n    language: python';
            const document = await createTestDocument(content, 'azure.yaml');

            const diagnostics = await provider.provideDiagnostics(document);

            assert.ok(diagnostics);
            const hostDiagnostic = diagnostics.find(d => d.message.includes('host'));
            assert.ok(hostDiagnostic);
            assert.equal(hostDiagnostic.severity, vscode.DiagnosticSeverity.Information);
        });

        test('warns about invalid host type', async () => {
            const content = 'name: myapp\nservices:\n  api:\n    project: ./api\n    host: invalidhost';
            const document = await createTestDocument(content, 'azure.yaml');

            const diagnostics = await provider.provideDiagnostics(document);

            assert.ok(diagnostics);
            const hostDiagnostic = diagnostics.find(d => d.message.includes('Invalid host type'));
            assert.ok(hostDiagnostic);
            assert.equal(hostDiagnostic.severity, vscode.DiagnosticSeverity.Warning);
            assert.ok(hostDiagnostic.message.includes('containerapp'));
            assert.ok(hostDiagnostic.message.includes('appservice'));
        });

        test('provides info for project path without ./ prefix', async () => {
            const content = 'name: myapp\nservices:\n  api:\n    project: api';
            const document = await createTestDocument(content, 'azure.yaml');

            const diagnostics = await provider.provideDiagnostics(document);

            assert.ok(diagnostics);
            const projectDiagnostic = diagnostics.find(d => d.message.includes('should start with'));
            assert.ok(projectDiagnostic);
            assert.equal(projectDiagnostic.severity, vscode.DiagnosticSeverity.Information);
        });

        test('accepts valid host types', async () => {
            const validHosts = ['containerapp', 'appservice', 'function', 'aks', 'staticwebapp'];

            for (const host of validHosts) {
                const content = `name: myapp\nservices:\n  api:\n    project: ./api\n    host: ${host}`;
                const document = await createTestDocument(content, 'azure.yaml');

                const diagnostics = await provider.provideDiagnostics(document);

                const hostDiagnostic = diagnostics?.find(d => d.message.includes('Invalid host type'));
                assert.isUndefined(hostDiagnostic, `${host} should be valid`);
            }
        });

        test('handles multiple services', async () => {
            const content = 'name: myapp\nservices:\n  api:\n    project: ./api\n  web:\n    project: ./web';
            const document = await createTestDocument(content, 'azure.yaml');

            const diagnostics = await provider.provideDiagnostics(document);

            assert.ok(diagnostics);
            // Should have diagnostics for both services missing language/host
            const apiDiagnostics = diagnostics.filter(d => d.message.includes('api'));
            const webDiagnostics = diagnostics.filter(d => d.message.includes('web'));
            assert.ok(apiDiagnostics.length > 0 || webDiagnostics.length > 0);
        });

        test('handles malformed YAML gracefully', async () => {
            const content = 'name: myapp\nservices\n  api:'; // Missing colon after services
            const document = await createTestDocument(content, 'azure.yaml');

            // Should not throw
            const diagnostics = await provider.provideDiagnostics(document);

            // May return undefined or empty array for malformed YAML
            assert.ok(diagnostics === undefined || Array.isArray(diagnostics));
        });

        test('returns no diagnostics for well-formed azure.yaml', async () => {
            // Mock file system to make project path appear to exist
            sandbox.stub(AzExtFsExtra, 'pathExists').resolves(true);

            const content = 'name: myapp\nservices:\n  api:\n    project: ./api\n    language: python\n    host: containerapp';
            const document = await createTestDocument(content, 'azure.yaml');

            const diagnostics = await provider.provideDiagnostics(document);

            // Should have no errors or warnings
            const errors = diagnostics?.filter(d => d.severity === vscode.DiagnosticSeverity.Error) || [];
            const warnings = diagnostics?.filter(d => d.severity === vscode.DiagnosticSeverity.Warning) || [];

            assert.equal(errors.length, 0);
            assert.equal(warnings.length, 0);
        });
    });

    async function createTestDocument(content: string, filename: string): Promise<vscode.TextDocument> {
        const uri = vscode.Uri.file(`/test/${filename}`);

        // Create a mock document
        const document = {
            uri,
            fileName: uri.fsPath,
            languageId: 'yaml',
            version: 1,
            lineCount: content.split('\n').length,
            getText: (range?: vscode.Range) => {
                if (!range) {
                    return content;
                }
                const lines = content.split('\n');
                return lines.slice(range.start.line, range.end.line + 1).join('\n');
            },
            lineAt: (line: number) => {
                const lines = content.split('\n');
                return {
                    text: lines[line] || '',
                    lineNumber: line,
                    range: new vscode.Range(line, 0, line, lines[line]?.length || 0),
                    rangeIncludingLineBreak: new vscode.Range(line, 0, line + 1, 0),
                    firstNonWhitespaceCharacterIndex: (lines[line] || '').search(/\S/),
                    isEmptyOrWhitespace: !(lines[line] || '').trim()
                };
            },
            positionAt: (offset: number) => {
                const lines = content.split('\n');
                let currentOffset = 0;
                for (let i = 0; i < lines.length; i++) {
                    if (currentOffset + lines[i].length >= offset) {
                        return new vscode.Position(i, offset - currentOffset);
                    }
                    currentOffset += lines[i].length + 1; // +1 for newline
                }
                return new vscode.Position(lines.length - 1, 0);
            },
            offsetAt: (position: vscode.Position) => {
                const lines = content.split('\n');
                let offset = 0;
                for (let i = 0; i < position.line; i++) {
                    offset += lines[i].length + 1;
                }
                return offset + position.character;
            },
            save: async () => true,
            eol: vscode.EndOfLine.LF,
            isDirty: false,
            isClosed: false,
            isUntitled: false,
            validateRange: (range: vscode.Range) => range,
            validatePosition: (position: vscode.Position) => position,
            getWordRangeAtPosition: (position: vscode.Position) => undefined
        } as unknown as vscode.TextDocument;

        return document;
    }
});
