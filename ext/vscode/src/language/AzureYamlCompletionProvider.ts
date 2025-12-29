// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

import * as vscode from 'vscode';
import * as yaml from 'yaml';

/**
 * Provides auto-completion for azure.yaml files
 */
export class AzureYamlCompletionProvider implements vscode.CompletionItemProvider {
    // Common Azure service host types
    private readonly hostTypes = [
        { label: 'containerapp', detail: 'Azure Container Apps', documentation: 'Deploy containerized applications' },
        { label: 'appservice', detail: 'Azure App Service', documentation: 'Deploy web applications' },
        { label: 'function', detail: 'Azure Functions', documentation: 'Deploy serverless functions' },
        { label: 'aks', detail: 'Azure Kubernetes Service', documentation: 'Deploy to Kubernetes cluster' },
        { label: 'staticwebapp', detail: 'Azure Static Web Apps', documentation: 'Deploy static web applications' },
    ];

    // Common hook types
    private readonly hookTypes = [
        { label: 'prerestore', documentation: 'Run before restoring dependencies' },
        { label: 'postrestore', documentation: 'Run after restoring dependencies' },
        { label: 'preprovision', documentation: 'Run before provisioning infrastructure' },
        { label: 'postprovision', documentation: 'Run after provisioning infrastructure' },
        { label: 'predeploy', documentation: 'Run before deploying application' },
        { label: 'postdeploy', documentation: 'Run after deploying application' },
    ];

    // Common service properties
    private readonly serviceProperties = [
        { label: 'project', detail: 'string', documentation: 'Relative path to the service project directory' },
        { label: 'language', detail: 'string', documentation: 'Programming language (e.g., js, ts, python, csharp, java)' },
        { label: 'host', detail: 'string', documentation: 'Azure hosting platform for the service' },
        { label: 'hooks', detail: 'object', documentation: 'Lifecycle hooks for the service' },
        { label: 'docker', detail: 'object', documentation: 'Docker configuration for containerized services' },
        { label: 'resourceName', detail: 'string', documentation: 'Name override for the Azure resource' },
    ];

    // Top-level properties
    private readonly topLevelProperties = [
        { label: 'name', detail: 'string', documentation: 'Application name' },
        { label: 'metadata', detail: 'object', documentation: 'Application metadata' },
        { label: 'services', detail: 'object', documentation: 'Service definitions' },
        { label: 'pipeline', detail: 'object', documentation: 'CI/CD pipeline configuration' },
        { label: 'hooks', detail: 'object', documentation: 'Application-level lifecycle hooks' },
    ];

    public provideCompletionItems(
        document: vscode.TextDocument,
        position: vscode.Position,
        token: vscode.CancellationToken,
        context: vscode.CompletionContext
    ): vscode.ProviderResult<vscode.CompletionItem[] | vscode.CompletionList> {
        const linePrefix = document.lineAt(position).text.substring(0, position.character);
        const yamlPath = this.getYamlPath(document, position);

        // Complete host types
        if (this.shouldCompleteHostType(linePrefix, yamlPath)) {
            return this.hostTypes.map(host => {
                const item = new vscode.CompletionItem(host.label, vscode.CompletionItemKind.Value);
                item.detail = host.detail;
                item.documentation = new vscode.MarkdownString(host.documentation);
                return item;
            });
        }

        // Complete hook types
        if (this.shouldCompleteHookType(yamlPath)) {
            return this.hookTypes.map(hook => {
                const item = new vscode.CompletionItem(hook.label, vscode.CompletionItemKind.Property);
                item.documentation = new vscode.MarkdownString(hook.documentation);
                item.insertText = new vscode.SnippetString(`${hook.label}:\n  run: \${1:command}\n  shell: \${2|sh,bash,pwsh|}\n  continueOnError: \${3|false,true|}`);
                return item;
            });
        }

        // Complete service properties
        if (this.shouldCompleteServiceProperty(yamlPath)) {
            return this.serviceProperties.map(prop => {
                const item = new vscode.CompletionItem(prop.label, vscode.CompletionItemKind.Property);
                item.detail = prop.detail;
                item.documentation = new vscode.MarkdownString(prop.documentation);

                if (prop.label === 'host') {
                    item.insertText = new vscode.SnippetString('host: ${1|containerapp,appservice,function,aks,staticwebapp|}');
                } else if (prop.label === 'project') {
                    item.insertText = new vscode.SnippetString('project: ./${1:path}');
                } else if (prop.label === 'language') {
                    item.insertText = new vscode.SnippetString('language: ${1|js,ts,python,csharp,java,go|}');
                }

                return item;
            });
        }

        // Complete top-level properties
        if (this.shouldCompleteTopLevelProperty(yamlPath)) {
            return this.topLevelProperties.map(prop => {
                const item = new vscode.CompletionItem(prop.label, vscode.CompletionItemKind.Property);
                item.detail = prop.detail;
                item.documentation = new vscode.MarkdownString(prop.documentation);

                if (prop.label === 'services') {
                    item.insertText = new vscode.SnippetString('services:\n  ${1:serviceName}:\n    project: ./${2:path}\n    language: ${3|js,ts,python,csharp,java|}\n    host: ${4|containerapp,appservice,function|}');
                }

                return item;
            });
        }

        return [];
    }

    private getYamlPath(document: vscode.TextDocument, position: vscode.Position): string[] {
        const text = document.getText(new vscode.Range(new vscode.Position(0, 0), position));
        try {
            // Parse document to validate YAML structure
            yaml.parseDocument(text);
            const path: string[] = [];

            // This is a simplified path detection - in production, you'd want more robust parsing
            const lines = text.split('\n');
            let currentIndent = 0;

            for (let i = position.line; i >= 0; i--) {
                const line = lines[i];
                const indent = line.search(/\S/);

                if (indent < 0) {
                    continue;
                }

                if (indent < currentIndent || currentIndent === 0) {
                    const match = line.match(/^\s*(\w+):/);
                    if (match) {
                        path.unshift(match[1]);
                        currentIndent = indent;
                    }
                }
            }

            return path;
        } catch {
            return [];
        }
    }

    private shouldCompleteHostType(linePrefix: string, yamlPath: string[]): boolean {
        return linePrefix.trim().startsWith('host:') ||
               (yamlPath.includes('services') && linePrefix.includes('host'));
    }

    private shouldCompleteHookType(yamlPath: string[]): boolean {
        return yamlPath.includes('hooks');
    }

    private shouldCompleteServiceProperty(yamlPath: string[]): boolean {
        return yamlPath.includes('services') && yamlPath.length >= 2 && !yamlPath.includes('hooks');
    }

    private shouldCompleteTopLevelProperty(yamlPath: string[]): boolean {
        return yamlPath.length === 0 || (yamlPath.length === 1 && yamlPath[0] === 'name');
    }
}
