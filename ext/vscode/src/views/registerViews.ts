// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

import * as vscode from 'vscode';
import { HelpAndFeedbackTreeDataProvider } from './helpAndFeedback/HelpAndFeedbackTreeDataProvider';
import { MyProjectTreeDataProvider } from './myProject/MyProjectTreeDataProvider';
import { EnvironmentsTreeDataProvider, EnvironmentTreeItem, EnvironmentItem } from './environments/EnvironmentsTreeDataProvider';
import { AzureDevCliEnvironmentVariable } from './workspace/AzureDevCliEnvironmentVariables';
import { ExtensionsTreeDataProvider } from './extensions/ExtensionsTreeDataProvider';
import { TemplateToolsTreeDataProvider } from './templateTools/TemplateToolsTreeDataProvider';
import { FileSystemWatcherService } from '../services/FileSystemWatcherService';

export function registerViews(context: vscode.ExtensionContext): void {
    // Create shared FileSystemWatcherService
    const fileSystemWatcherService = new FileSystemWatcherService();
    context.subscriptions.push(fileSystemWatcherService);

    const helpAndFeedbackProvider = new HelpAndFeedbackTreeDataProvider();
    context.subscriptions.push(
        vscode.window.registerTreeDataProvider('azure-dev.views.helpAndFeedback', helpAndFeedbackProvider)
    );

    const myProjectProvider = new MyProjectTreeDataProvider(fileSystemWatcherService);
    context.subscriptions.push(
        vscode.window.registerTreeDataProvider('azure-dev.views.myProject', myProjectProvider)
    );
    context.subscriptions.push(myProjectProvider);

    const environmentsProvider = new EnvironmentsTreeDataProvider(fileSystemWatcherService);
    context.subscriptions.push(
        vscode.window.registerTreeDataProvider('azure-dev.views.environments', environmentsProvider)
    );
    context.subscriptions.push(environmentsProvider);
    context.subscriptions.push(
        vscode.commands.registerCommand('azure-dev.views.environments.refresh', () => {
            environmentsProvider.refresh();
        })
    );

    const extensionsProvider = new ExtensionsTreeDataProvider();
    context.subscriptions.push(
        vscode.window.registerTreeDataProvider('azure-dev.views.extensions', extensionsProvider)
    );
    context.subscriptions.push(
        vscode.commands.registerCommand('azure-dev.views.extensions.refresh', () => {
            extensionsProvider.refresh();
        })
    );

    context.subscriptions.push(
        vscode.commands.registerCommand('azure-dev.views.environments.toggleEnvVarVisibility', (item: EnvironmentTreeItem) => {
            environmentsProvider.toggleVisibility(item);
        })
    );

    context.subscriptions.push(
        vscode.commands.registerCommand('azure-dev.views.workspace.toggleEnvVarVisibility', (item: AzureDevCliEnvironmentVariable) => {
            item.toggleVisibility();
        })
    );

    context.subscriptions.push(
        vscode.commands.registerCommand('azure-dev.views.environments.viewDotEnv', (item: EnvironmentTreeItem) => {
            if (item.data && (item.data as EnvironmentItem).dotEnvPath) {
                const envItem = item.data as EnvironmentItem;
                if (envItem.dotEnvPath) {
                    void vscode.commands.executeCommand('vscode.open', vscode.Uri.file(envItem.dotEnvPath));
                }
            }
        })
    );

    const templateToolsProvider = new TemplateToolsTreeDataProvider(fileSystemWatcherService);
    context.subscriptions.push(
        vscode.window.registerTreeDataProvider('azure-dev.views.templateTools', templateToolsProvider)
    );
    context.subscriptions.push(templateToolsProvider);
    context.subscriptions.push(
        vscode.commands.registerCommand('azure-dev.views.templateTools.refresh', () => {
            templateToolsProvider.refresh();
        })
    );
}
