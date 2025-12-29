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

                // Additional validation checks
                results.push(...this.validateYamlStructure(document));
            } catch {
                // Best effort--the YAML extension will show parsing errors for us if it is present
            }

            context.telemetry.measurements.diagnosticCount = results.length;
            return results;
        });
    }

    private validateYamlStructure(document: vscode.TextDocument): vscode.Diagnostic[] {
        const diagnostics: vscode.Diagnostic[] = [];
        const text = document.getText();

        try {
            const yaml = require('yaml');
            const doc = yaml.parseDocument(text);

            if (!doc || doc.errors.length > 0) {
                return diagnostics;
            }

            const content = doc.toJSON();

            // Validate required name property
            if (!content.name) {
                diagnostics.push(new vscode.Diagnostic(
                    new vscode.Range(0, 0, 0, 0),
                    vscode.l10n.t('Missing required "name" property. Add a name for your application.'),
                    vscode.DiagnosticSeverity.Warning
                ));
            }

            // Validate services structure
            if (content.services) {
                for (const [serviceName, service] of Object.entries(content.services as Record<string, any>)) {
                    const serviceLineNumber = this.findLineNumber(text, serviceName);

                    // Warn about missing language
                    if (!service.language) {
                        diagnostics.push(new vscode.Diagnostic(
                            new vscode.Range(serviceLineNumber, 0, serviceLineNumber, 100),
                            vscode.l10n.t('Service "{0}" is missing "language" property. This helps azd understand your project.', serviceName),
                            vscode.DiagnosticSeverity.Information
                        ));
                    }

                    // Warn about missing host
                    if (!service.host) {
                        diagnostics.push(new vscode.Diagnostic(
                            new vscode.Range(serviceLineNumber, 0, serviceLineNumber, 100),
                            vscode.l10n.t('Service "{0}" is missing "host" property. Specify the Azure platform for deployment.', serviceName),
                            vscode.DiagnosticSeverity.Information
                        ));
                    }

                    // Validate host value
                    if (service.host) {
                        const validHosts = ['containerapp', 'appservice', 'function', 'aks', 'staticwebapp'];
                        if (!validHosts.includes(service.host)) {
                            const hostLineNumber = this.findLineNumber(text, 'host:', serviceLineNumber);
                            diagnostics.push(new vscode.Diagnostic(
                                new vscode.Range(hostLineNumber, 0, hostLineNumber, 100),
                                vscode.l10n.t('Invalid host type "{0}". Valid options: {1}', service.host, validHosts.join(', ')),
                                vscode.DiagnosticSeverity.Warning
                            ));
                        }
                    }

                    // Validate project path format
                    if (service.project && !service.project.startsWith('./')) {
                        const projectLineNumber = this.findLineNumber(text, 'project:', serviceLineNumber);
                        diagnostics.push(new vscode.Diagnostic(
                            new vscode.Range(projectLineNumber, 0, projectLineNumber, 100),
                            vscode.l10n.t('Project paths should start with "./" for clarity.'),
                            vscode.DiagnosticSeverity.Information
                        ));
                    }
                }
            }
        } catch {
            // Ignore parsing errors - YAML extension handles those
        }

        return diagnostics;
    }

    private findLineNumber(text: string, searchString: string, startLine: number = 0): number {
        const lines = text.split('\n');
        for (let i = startLine; i < lines.length; i++) {
            if (lines[i].includes(searchString)) {
                return i;
            }
        }
        return startLine;
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
