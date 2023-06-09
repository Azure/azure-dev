// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

import * as vscode from 'vscode';
import ext from '../ext';
import { AzureYamlCompletionItemProvider } from './AzureYamlCompletionItemProvider';
import { IActionContext, registerEvent } from '@microsoft/vscode-azext-utils';

export function registerLanguageFeatures(): void {
    const selector: vscode.DocumentSelector = { language: 'yaml', scheme: 'file', pattern: '**/azure.{yml,yaml}' };

    ext.context.subscriptions.push(
        vscode.languages.registerCompletionItemProvider(selector, new AzureYamlCompletionItemProvider(), '/'),
    );

    let azureYamlDiagnosticCollection: vscode.DiagnosticCollection;
    ext.context.subscriptions.push(
        azureYamlDiagnosticCollection = vscode.languages.createDiagnosticCollection('azure.yml')
    );

    registerEvent('onDidChangeTextDocument', vscode.workspace.onDidChangeTextDocument, async (context: IActionContext, e: vscode.TextDocumentChangeEvent) => {
        if (e.document.languageId !== 'yaml' || !/azure\.ya?ml$/i.test(e.document.fileName)) {
            return;
        }

        context.telemetry.suppressAll = true;
        context.errorHandling.suppressReportIssue = true;

        // Rerun diagnostics
    });

    registerEvent('onDidRenameFiles', vscode.workspace.onDidRenameFiles, async (context: IActionContext, e: vscode.FileRenameEvent) => {
        context.telemetry.suppressAll = true;
        context.errorHandling.suppressReportIssue = true;

        // Noop
    });
}