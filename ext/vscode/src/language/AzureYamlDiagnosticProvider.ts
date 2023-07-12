// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

import { AzExtFsExtra, IActionContext, callWithTelemetryAndErrorHandling } from '@microsoft/vscode-azext-utils';
import * as vscode from 'vscode';
import { documentDebounce } from './documentDebounce';
import { getAzureYamlProjectInformation } from './azureYamlUtils';
import { TelemetryId } from '../telemetry/telemetryId';

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

        disposables.push(vscode.workspace.onDidChangeTextDocument((e: vscode.TextDocumentChangeEvent) => this.updateDiagnosticsFor(e.document)));
        disposables.push(vscode.workspace.onDidRenameFiles(() => this.updateDiagnosticsForOpenTabs()));
        disposables.push(vscode.workspace.onDidCreateFiles(() => this.updateDiagnosticsForOpenTabs()));
        disposables.push(vscode.workspace.onDidDeleteFiles(() => this.updateDiagnosticsForOpenTabs()));
        disposables.push(vscode.window.onDidChangeVisibleTextEditors(() => this.updateDiagnosticsForOpenTabs()));

        super(() => {
            vscode.Disposable.from(...disposables).dispose();
        });

        this.diagnosticCollection = diagnosticCollection;
    }

    public provideDiagnostics(document: vscode.TextDocument, token?: vscode.CancellationToken): Promise<vscode.Diagnostic[] | undefined> {
        return callWithTelemetryAndErrorHandling(TelemetryId.AzureYamlProvideDiagnostics, async (context: IActionContext) => {
            const results: vscode.Diagnostic[] = [];

            try {
                const projectInformation = await getAzureYamlProjectInformation(document);

                for (const project of projectInformation) {
                    if (await AzExtFsExtra.pathExists(project.projectUri)) {
                        continue;
                    }

                    const diagnostic = new vscode.Diagnostic(
                        project.projectValueNodeRange,
                        vscode.l10n.t('The project path must be an existing folder or file path relative to the azure.yaml file.'),
                        vscode.DiagnosticSeverity.Error
                    );

                    results.push(diagnostic);
                }
            } catch {
                // Best effort--the YAML extension will show parsing errors for us if it is present
            }

            context.telemetry.measurements.diagnosticCount = results.length;
            return results;
        });
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
