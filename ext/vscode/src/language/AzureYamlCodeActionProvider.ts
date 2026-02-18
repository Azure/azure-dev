// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

import { AzExtFsExtra } from '@microsoft/vscode-azext-utils';
import * as vscode from 'vscode';
import * as yaml from 'yaml';
import { SUPPORTED_LANGUAGES } from '../constants/languages';
import { getContainingFolderUri } from './azureYamlUtils';

/**
 * Provides code actions (quick fixes) for azure.yaml files
 */
export class AzureYamlCodeActionProvider implements vscode.CodeActionProvider {
    public static readonly providedCodeActionKinds = [
        vscode.CodeActionKind.QuickFix
    ];

    public async provideCodeActions(
        document: vscode.TextDocument,
        range: vscode.Range | vscode.Selection,
        context: vscode.CodeActionContext,
        token: vscode.CancellationToken
    ): Promise<vscode.CodeAction[]> {
        const actions: vscode.CodeAction[] = [];

        for (const diagnostic of context.diagnostics) {
            // Quick fix for missing project paths
            if (diagnostic.message.includes('project path must be an existing')) {
                actions.push(this.createCreateFolderAction(document, diagnostic));
                actions.push(this.createBrowseForFolderAction(document, diagnostic));
            }

            // Quick fix for missing language property
            if (diagnostic.message.includes('language') && diagnostic.message.includes('missing')) {
                actions.push(...this.createAddLanguageActions(document, diagnostic));
            }

            // Quick fix for missing host property
            if (diagnostic.message.includes('host') && diagnostic.message.includes('missing')) {
                actions.push(...this.createAddHostActions(document, diagnostic));
            }
        }

        // Add general code actions
        actions.push(...await this.provideGeneralActions(document, range));

        return actions;
    }

    private createCreateFolderAction(document: vscode.TextDocument, diagnostic: vscode.Diagnostic): vscode.CodeAction {
        const action = new vscode.CodeAction(vscode.l10n.t('Create folder'), vscode.CodeActionKind.QuickFix);
        action.diagnostics = [diagnostic];
        action.isPreferred = true;

        const projectPath = this.extractProjectPath(document, diagnostic.range);
        if (projectPath) {
            action.command = {
                title: vscode.l10n.t('Create folder'),
                command: 'azure-dev.codeAction.createProjectFolder',
                arguments: [document.uri, projectPath]
            };
        }

        return action;
    }

    private createBrowseForFolderAction(document: vscode.TextDocument, diagnostic: vscode.Diagnostic): vscode.CodeAction {
        const action = new vscode.CodeAction(vscode.l10n.t('Browse for existing folder...'), vscode.CodeActionKind.QuickFix);
        action.diagnostics = [diagnostic];

        action.command = {
            title: vscode.l10n.t('Browse for folder'),
            command: 'azure-dev.codeAction.browseForProjectFolder',
            arguments: [document.uri, diagnostic.range]
        };

        return action;
    }

    private createAddLanguageActions(document: vscode.TextDocument, diagnostic: vscode.Diagnostic): vscode.CodeAction[] {
        return SUPPORTED_LANGUAGES.map(lang => {
            const action = new vscode.CodeAction(vscode.l10n.t('Add language: {0}', lang), vscode.CodeActionKind.QuickFix);
            action.diagnostics = [diagnostic];
            action.edit = new vscode.WorkspaceEdit();

            // Find the line to insert the language property
            const insertPosition = new vscode.Position(diagnostic.range.start.line + 1, diagnostic.range.start.character);
            action.edit.insert(document.uri, insertPosition, `  language: ${lang}\n`);

            return action;
        });
    }

    private createAddHostActions(document: vscode.TextDocument, diagnostic: vscode.Diagnostic): vscode.CodeAction[] {
        const hosts = [
            { value: 'containerapp', label: vscode.l10n.t('Container Apps') },
            { value: 'appservice', label: vscode.l10n.t('App Service') },
            { value: 'function', label: vscode.l10n.t('Functions') }
        ];

        return hosts.map(host => {
            const action = new vscode.CodeAction(vscode.l10n.t('Add host: {0}', host.label), vscode.CodeActionKind.QuickFix);
            action.diagnostics = [diagnostic];
            action.edit = new vscode.WorkspaceEdit();

            const insertPosition = new vscode.Position(diagnostic.range.start.line + 1, diagnostic.range.start.character);
            action.edit.insert(document.uri, insertPosition, `  host: ${host.value}\n`);

            return action;
        });
    }

    private provideGeneralActions(document: vscode.TextDocument, range: vscode.Range): Promise<vscode.CodeAction[]> {
        const actions: vscode.CodeAction[] = [];

        // Add "Add new service" refactoring action
        const addServiceAction = new vscode.CodeAction(vscode.l10n.t('Add new service...'), vscode.CodeActionKind.Refactor);
        addServiceAction.command = {
            title: vscode.l10n.t('Add new service'),
            command: 'azure-dev.codeAction.addService',
            arguments: [document.uri]
        };
        actions.push(addServiceAction);

        return Promise.resolve(actions);
    }

    private extractProjectPath(document: vscode.TextDocument, range: vscode.Range): string | undefined {
        try {
            const line = document.lineAt(range.start.line);
            // eslint-disable-next-line @typescript-eslint/prefer-regexp-exec
            const match = line.text.match(/project:\s*(.+)/);
            return match ? match[1].trim().replace(/['"]/g, '') : undefined;
        } catch {
            return undefined;
        }
    }
}

/**
 * Code action command handlers
 */
export function registerCodeActionCommands(context: vscode.ExtensionContext): void {
    context.subscriptions.push(
        vscode.commands.registerCommand('azure-dev.codeAction.createProjectFolder', async (documentUri: vscode.Uri, projectPath: string) => {
            try {
                const folderUri = vscode.Uri.joinPath(getContainingFolderUri(documentUri), projectPath);
                await AzExtFsExtra.ensureDir(folderUri.fsPath);
                void vscode.window.showInformationMessage(vscode.l10n.t('Created folder: {0}', projectPath));
            } catch (error) {
                void vscode.window.showErrorMessage(vscode.l10n.t('Failed to create folder: {0}', error instanceof Error ? error.message : String(error)));
            }
        })
    );

    context.subscriptions.push(
        vscode.commands.registerCommand('azure-dev.codeAction.browseForProjectFolder', async (documentUri: vscode.Uri, range: vscode.Range) => {
            const workspaceFolder = vscode.workspace.getWorkspaceFolder(documentUri);
            const selected = await vscode.window.showOpenDialog({
                canSelectFiles: false,
                canSelectFolders: true,
                canSelectMany: false,
                defaultUri: workspaceFolder?.uri,
                openLabel: vscode.l10n.t('Select Project Folder')
            });

            if (selected?.[0]) {
                const relativePath = vscode.workspace.asRelativePath(selected[0], false);
                const document = await vscode.workspace.openTextDocument(documentUri);
                const edit = new vscode.WorkspaceEdit();

                const line = document.lineAt(range.start.line);
                // eslint-disable-next-line @typescript-eslint/prefer-regexp-exec
                const match = line.text.match(/project:\s*.+/);
                if (match) {
                    const replaceRange = new vscode.Range(
                        range.start.line,
                        line.text.indexOf(match[0]) + 'project: '.length,
                        range.start.line,
                        line.text.length
                    );
                    edit.replace(documentUri, replaceRange, `./${relativePath}`);
                    await vscode.workspace.applyEdit(edit);
                }
            }
        })
    );

    context.subscriptions.push(
        vscode.commands.registerCommand('azure-dev.codeAction.addService', async (documentUri: vscode.Uri) => {
            const serviceName = await vscode.window.showInputBox({
                prompt: vscode.l10n.t('Enter service name'),
                placeHolder: 'api',
                validateInput: (value) => {
                    if (!value || !/^[a-zA-Z0-9-_]+$/.test(value)) {
                        return vscode.l10n.t('Service name must contain only letters, numbers, hyphens, and underscores');
                    }
                    return undefined;
                }
            });

            if (!serviceName) {
                return;
            }

            const language = await vscode.window.showQuickPick(
                [...SUPPORTED_LANGUAGES],
                { placeHolder: vscode.l10n.t('Select programming language') }
            );

            if (!language) {
                return;
            }

            const host = await vscode.window.showQuickPick(
                [
                    { label: 'containerapp', description: vscode.l10n.t('Azure Container Apps') },
                    { label: 'appservice', description: vscode.l10n.t('Azure App Service') },
                    { label: 'function', description: vscode.l10n.t('Azure Functions') }
                ],
                { placeHolder: vscode.l10n.t('Select Azure host') }
            );

            if (!host) {
                return;
            }

            const document = await vscode.workspace.openTextDocument(documentUri);
            const text = document.getText();
            const doc = yaml.parseDocument(text);

            const services = doc.get('services') as yaml.YAMLMap;
            if (!services) {
                void vscode.window.showErrorMessage(vscode.l10n.t('No services section found in azure.yaml'));
                return;
            }

            const serviceSnippet = `\n  ${serviceName}:\n    project: ./${serviceName}\n    language: ${language}\n    host: ${host.label}`;

            // Find the end of the services section
            if (doc.contents && yaml.isMap(doc.contents)) {
                const servicesNode = doc.contents.items.find((item) => yaml.isScalar(item.key) && item.key.value === 'services');
                if (servicesNode?.value && 'range' in servicesNode.value && servicesNode.value.range) {
                    const insertPosition = document.positionAt(servicesNode.value.range[1]);
                    const edit = new vscode.WorkspaceEdit();
                    edit.insert(documentUri, insertPosition, serviceSnippet);
                    await vscode.workspace.applyEdit(edit);
                }
            }
        })
    );
}
