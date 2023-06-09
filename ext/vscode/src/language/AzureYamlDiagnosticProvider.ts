// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

import * as vscode from 'vscode';
import * as yaml from 'yaml';
import { documentDebounce } from './documentDebounce';

// Time between when the user stops typing and when we send diagnostics
const DiagnosticDelay = 1000;

export class AzureYamlDiagnosticProvider extends vscode.Disposable {
    private readonly diagnosticCollection: vscode.DiagnosticCollection;

    public constructor(
        private readonly selector: vscode.DocumentSelector
    ) {
        const disposables: vscode.Disposable[] = [];

        const diagnosticCollection = vscode.languages.createDiagnosticCollection('azure.yaml');
        disposables.push(diagnosticCollection);
    
        disposables.push(vscode.workspace.onDidChangeTextDocument((e: vscode.TextDocumentChangeEvent) => this.updateDiagnosticsFor(e.document))); // TODO: do a delayed debounce
        disposables.push(vscode.workspace.onDidRenameFiles(() => this.updateDiagnosticsForOpenTabs()));
        disposables.push(vscode.window.onDidChangeVisibleTextEditors(() => this.updateDiagnosticsForOpenTabs()));

        super(() => {
            vscode.Disposable.from(...disposables).dispose();
        });

        this.diagnosticCollection = diagnosticCollection;
    }

    public async provideDiagnostics(document: vscode.TextDocument, token?: vscode.CancellationToken): Promise<vscode.Diagnostic[] | undefined> {
        const results: vscode.Diagnostic[] = [];

        try {
            // Parse the document
            const yamlDocument = yaml.parseDocument(document.getText()) as yaml.Document;
            if (!yamlDocument || yamlDocument.errors.length > 0) {
                throw new Error(vscode.l10n.t('Unable to parse {0}', document.uri.toString()));
            }

            const services = yamlDocument.get('services') as yaml.YAMLMap<yaml.Scalar, yaml.YAMLMap>;

            // For each service, ensure that a directory exists matching the relative path specified for the service
            for (const service of services?.items || []) {
                const projectNode = service.value?.get('project', true) as yaml.Scalar<string>;
                const projectPath = projectNode?.value;

                if (!projectPath) {
                    continue;
                } else {
                    const projectFolder = vscode.Uri.joinPath(document.uri, '..', projectPath);
                    
                    try {
                        const fstat = await vscode.workspace.fs.stat(projectFolder);

                        if (fstat.type === vscode.FileType.Directory) {
                            continue;
                        }
                    } catch {
                        // Suppress the error--we'll emit our diagnostic below
                    }
                }

                // If not, then emit an error diagnostic about it
                results.push(new vscode.Diagnostic(
                    new vscode.Range(document.positionAt(projectNode.range?.[0] ?? 0), document.positionAt(projectNode.range?.[1] ?? 0)),
                    vscode.l10n.t('The project path must be an existing folder path relative to the azure.yaml file.'),
                    vscode.DiagnosticSeverity.Error
                ));
            }
        } catch {
            // Best effort--the YAML extension will show parsing errors for us if it is present
            return results;
        }

        return results;
    }

    private async updateDiagnosticsFor(document: vscode.TextDocument, delay: boolean = true): Promise<void> {
        if (!vscode.languages.match(this.selector, document)) {
            return;
        }

        const method = async () => {
            this.diagnosticCollection.delete(document.uri);
            this.diagnosticCollection.set(document.uri, await this.provideDiagnostics(document));
        };

        if (delay) { 
            documentDebounce(DiagnosticDelay, { uri: document.uri, callId: 'updateDiagnosticsFor' }, method, this);
        } else {
            await method();
        }
        
    }

    private async updateDiagnosticsForOpenTabs(): Promise<void> {
        await Promise.all<void>(vscode.window.visibleTextEditors.map(async (editor: vscode.TextEditor) => {
            await this.updateDiagnosticsFor(editor.document, false);
        }));
    }
}
