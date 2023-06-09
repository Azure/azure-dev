// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

import * as vscode from 'vscode';
import ext from '../ext';
import { AzureYamlCompletionItemProvider } from './AzureYamlCompletionItemProvider';
import { AzureYamlDiagnosticProvider } from './AzureYamlDiagnosticProvider';
import { AzureYamlCodeActionProvider } from './AzureYamlCodeActionProvider';

export function registerLanguageFeatures(): void {
    const selector: vscode.DocumentSelector = { language: 'yaml', scheme: 'file', pattern: '**/azure.{yml,yaml}' };

    ext.context.subscriptions.push(
        vscode.languages.registerCompletionItemProvider(selector, new AzureYamlCompletionItemProvider(), '/'),
    );

    ext.context.subscriptions.push(
        vscode.languages.registerCodeActionsProvider(selector, new AzureYamlCodeActionProvider(), { providedCodeActionKinds: [vscode.CodeActionKind.QuickFix, vscode.CodeActionKind.RefactorMove] })
    );

    ext.context.subscriptions.push(
        new AzureYamlDiagnosticProvider(selector)
    );
}