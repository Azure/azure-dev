// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

import * as vscode from 'vscode';
import ext from '../ext';
import { AzureYamlDiagnosticProvider } from './AzureYamlDiagnosticProvider';

export function registerLanguageFeatures(): void {
    const selector: vscode.DocumentSelector = { language: 'yaml', scheme: 'file', pattern: '**/azure.{yml,yaml}' };

    ext.context.subscriptions.push(
        new AzureYamlDiagnosticProvider(selector)
    );
}