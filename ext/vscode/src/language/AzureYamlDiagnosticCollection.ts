// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

import * as vscode from 'vscode';
import ext from '../ext';
import { IActionContext, registerEvent } from '@microsoft/vscode-azext-utils';

export let azureYamlDiagnosticCollection: vscode.DiagnosticCollection;

export function registerDiagnosticCollection(): void {
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