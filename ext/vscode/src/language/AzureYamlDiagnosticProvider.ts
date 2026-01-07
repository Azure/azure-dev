// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

import { AzExtFsExtra, IActionContext, callWithTelemetryAndErrorHandling } from '@microsoft/vscode-azext-utils';
import * as vscode from 'vscode';
import * as yaml from 'yaml';
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
            const text = document.getText();

            // Check for empty file first
            if (!text || text.trim().length === 0) {
                const diagnostic = new vscode.Diagnostic(
                    new vscode.Range(0, 0, 0, 0),
                    vscode.l10n.t('azure.yaml file is empty. Add configuration like:\n\nname: my-app\nservices:\n  web:\n    project: ./src\n    language: python\n    host: containerapp'),
                    vscode.DiagnosticSeverity.Error
                );
                diagnostic.code = {
                    value: 'azure-yaml-empty',
                    target: vscode.Uri.parse('https://aka.ms/azure-dev/schema')
                };
                results.push(diagnostic);
                context.telemetry.measurements.diagnosticCount = results.length;
                context.telemetry.properties.isEmpty = 'true';
                return results;
            }

            try {
                // Try to parse the YAML first to provide friendly errors
                const parseResult = this.validateYamlParsing(document);
                if (parseResult.length > 0) {
                    results.push(...parseResult);
                    context.telemetry.measurements.diagnosticCount = results.length;
                    context.telemetry.properties.hasParseErrors = 'true';
                    return results;
                }

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
                // If we can't parse, provide a helpful error
                const diagnostic = new vscode.Diagnostic(
                    new vscode.Range(0, 0, 0, 0),
                    vscode.l10n.t('Unable to parse azure.yaml. Check for YAML syntax errors.'),
                    vscode.DiagnosticSeverity.Error
                );
                diagnostic.code = {
                    value: 'azure-yaml-parse-error',
                    target: vscode.Uri.parse('https://aka.ms/azure-dev/schema')
                };
                results.push(diagnostic);
            }

            context.telemetry.measurements.diagnosticCount = results.length;
            return results;
        });
    }

    private validateYamlParsing(document: vscode.TextDocument): vscode.Diagnostic[] {
        const diagnostics: vscode.Diagnostic[] = [];
        const text = document.getText();

        try {
            const doc = yaml.parseDocument(text);

            // Check for YAML parsing errors
            if (doc.errors && doc.errors.length > 0) {
                for (const error of doc.errors) {
                    let range: vscode.Range;
                    let message = error.message;

                    // Try to get position from error
                    if (error.pos && error.pos.length >= 2) {
                        const start = document.positionAt(error.pos[0]);
                        const end = document.positionAt(error.pos[1] || error.pos[0]);
                        range = new vscode.Range(start, end);
                    } else {
                        range = new vscode.Range(0, 0, 0, 0);
                    }

                    // Provide more user-friendly messages
                    if (message.includes('Unexpected token') || message.includes('Plain value cannot start with block scalar indicator')) {
                        message = vscode.l10n.t('YAML syntax error: {0}. Check your indentation and formatting.', message);
                    }

                    const diagnostic = new vscode.Diagnostic(
                        range,
                        message,
                        vscode.DiagnosticSeverity.Error
                    );
                    diagnostic.code = {
                        value: 'azure-yaml-syntax',
                        target: vscode.Uri.parse('https://aka.ms/azure-dev/schema')
                    };
                    diagnostics.push(diagnostic);
                }
                return diagnostics;
            }

            // Check for warnings
            if (doc.warnings && doc.warnings.length > 0) {
                for (const warning of doc.warnings) {
                    let range: vscode.Range;
                    if (warning.pos && warning.pos.length >= 2) {
                        const start = document.positionAt(warning.pos[0]);
                        const end = document.positionAt(warning.pos[1] || warning.pos[0]);
                        range = new vscode.Range(start, end);
                    } else {
                        range = new vscode.Range(0, 0, 0, 0);
                    }

                    const diagnostic = new vscode.Diagnostic(
                        range,
                        warning.message,
                        vscode.DiagnosticSeverity.Warning
                    );
                    diagnostics.push(diagnostic);
                }
            }
        } catch {
            // Fatal parsing error
            const diagnostic = new vscode.Diagnostic(
                new vscode.Range(0, 0, 0, 0),
                vscode.l10n.t('Unable to parse azure.yaml file. Ensure the file contains valid YAML syntax.'),
                vscode.DiagnosticSeverity.Error
            );
            diagnostic.code = {
                value: 'azure-yaml-fatal',
                target: vscode.Uri.parse('https://aka.ms/azure-dev/schema')
            };
            diagnostics.push(diagnostic);
        }

        return diagnostics;
    }

    private validateYamlStructure(document: vscode.TextDocument): vscode.Diagnostic[] {
        const diagnostics: vscode.Diagnostic[] = [];
        const text = document.getText();

        try {
            const doc = yaml.parseDocument(text);

            if (!doc || doc.errors.length > 0) {
                return diagnostics;
            }

            const content = doc.toJSON();

            // If content is null or not an object, the file structure is invalid
            if (!content || typeof content !== 'object') {
                diagnostics.push(new vscode.Diagnostic(
                    new vscode.Range(0, 0, 0, 0),
                    vscode.l10n.t('azure.yaml must contain a valid YAML object. Start with:\n\nname: my-app\nservices:\n  web:\n    project: ./src'),
                    vscode.DiagnosticSeverity.Error
                ));
                return diagnostics;
            }

            // Validate required name property
            if (!content.name) {
                diagnostics.push(new vscode.Diagnostic(
                    new vscode.Range(0, 0, 0, 0),
                    vscode.l10n.t('Missing required "name" property. Add a name for your application.'),
                    vscode.DiagnosticSeverity.Error
                ));
            }

            // Validate that name is not empty
            if (content.name && typeof content.name === 'string' && content.name.trim().length === 0) {
                const nameLine = this.findLineNumber(text, 'name:');
                diagnostics.push(new vscode.Diagnostic(
                    new vscode.Range(nameLine, 0, nameLine, 100),
                    vscode.l10n.t('Application name cannot be empty.'),
                    vscode.DiagnosticSeverity.Error
                ));
            }

            // Validate services exist
            if (!content.services || typeof content.services !== 'object' || Object.keys(content.services).length === 0) {
                const servicesLine = this.findLineNumber(text, 'services:');
                diagnostics.push(new vscode.Diagnostic(
                    new vscode.Range(servicesLine >= 0 ? servicesLine : 0, 0, servicesLine >= 0 ? servicesLine : 0, 100),
                    vscode.l10n.t('No services defined. Add at least one service to deploy.'),
                    vscode.DiagnosticSeverity.Error
                ));
                return diagnostics;
            }

            // Validate services structure
            if (content.services) {
                for (const [serviceName, service] of Object.entries(content.services as Record<string, { language?: string; host?: string; project?: string }>)) {
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
