// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

import { assert } from 'chai';
import * as vscode from 'vscode';
import * as sinon from 'sinon';
import { AzExtFsExtra } from '@microsoft/vscode-azext-utils';
import { AzureYamlDiagnosticProvider } from '../../../language/AzureYamlDiagnosticProvider';

/**
 * Tests for AzureYamlDiagnosticProvider
 *
 * Note: Schema validation (required properties, valid values, etc.) is handled by
 * the RedHat YAML extension using the azure.yaml JSON schema. This provider only
 * handles validation that requires runtime checks, such as verifying that project
 * paths exist on disk.
 */
suite('AzureYamlDiagnosticProvider - Project Path Validation', () => {
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

    suite('project path validation', () => {
        test('returns no diagnostics when project paths exist', async () => {
            // Mock file system to make project path appear to exist
            sandbox.stub(AzExtFsExtra, 'pathExists').resolves(true);

            const content = 'name: myapp\nservices:\n  api:\n    project: ./api\n    language: python\n    host: containerapp';
            const document = await createTestDocument(content, 'azure.yaml');

            const diagnostics = await provider.provideDiagnostics(document);

            assert.ok(diagnostics);
            assert.equal(diagnostics.length, 0);
        });

        test('returns error diagnostic when project path does not exist', async () => {
            // Mock file system to make project path appear to not exist
            sandbox.stub(AzExtFsExtra, 'pathExists').resolves(false);

            const content = 'name: myapp\nservices:\n  api:\n    project: ./nonexistent\n    language: python\n    host: containerapp';
            const document = await createTestDocument(content, 'azure.yaml');

            const diagnostics = await provider.provideDiagnostics(document);

            assert.ok(diagnostics);
            const pathDiagnostic = diagnostics.find(d => d.message.includes('project path'));
            assert.ok(pathDiagnostic);
            assert.equal(pathDiagnostic.severity, vscode.DiagnosticSeverity.Error);
        });

        test('handles YAML parsing errors gracefully', async () => {
            const content = 'name: myapp\nservices\n  api:'; // Invalid YAML
            const document = await createTestDocument(content, 'azure.yaml');

            // Should not throw
            const diagnostics = await provider.provideDiagnostics(document);

            // May return undefined or empty array for malformed YAML
            assert.ok(diagnostics === undefined || Array.isArray(diagnostics));
        });

        test('handles empty file gracefully', async () => {
            const content = '';
            const document = await createTestDocument(content, 'azure.yaml');

            // Should not throw
            const diagnostics = await provider.provideDiagnostics(document);

            // May return undefined or empty array for empty file
            assert.ok(diagnostics === undefined || Array.isArray(diagnostics));
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
