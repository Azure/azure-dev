// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

import * as vscode from 'vscode';
import ext from '../ext';
import { AzureYamlDiagnosticProvider } from './AzureYamlDiagnosticProvider';
import { AzureYamlProjectRenameProvider } from './AzureYamlProjectRenameProvider';
import { AzureYamlDocumentDropEditProvider } from './AzureYamlDocumentDropEditProvider';
import { AzureYamlCodeActionProvider, registerCodeActionCommands } from './AzureYamlCodeActionProvider';

export const AzureYamlSelector: vscode.DocumentSelector = { language: 'yaml', scheme: 'file', pattern: '**/azure.{yml,yaml}' };

export function registerLanguageFeatures(): void {
    ext.context.subscriptions.push(
        new AzureYamlDiagnosticProvider(AzureYamlSelector)
    );

    ext.context.subscriptions.push(
        new AzureYamlProjectRenameProvider()
    );

    ext.context.subscriptions.push(
        vscode.languages.registerDocumentDropEditProvider(AzureYamlSelector, new AzureYamlDocumentDropEditProvider())
    );

    // Register code action provider
    ext.context.subscriptions.push(
        vscode.languages.registerCodeActionsProvider(
            AzureYamlSelector,
            new AzureYamlCodeActionProvider(),
            {
                providedCodeActionKinds: AzureYamlCodeActionProvider.providedCodeActionKinds
            }
        )
    );

    // Register code action commands
    registerCodeActionCommands(ext.context);
}
