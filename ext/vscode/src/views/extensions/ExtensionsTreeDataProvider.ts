// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

import * as vscode from 'vscode';
import { callWithTelemetryAndErrorHandling } from '@microsoft/vscode-azext-utils';
import { TelemetryId } from '../../telemetry/telemetryId';
import { WorkspaceAzureDevExtensionProvider, AzureDevExtension } from '../../services/AzureDevExtensionProvider';

export class ExtensionTreeItem extends vscode.TreeItem {
    constructor(
        public readonly extension: AzureDevExtension
    ) {
        super(extension.name, vscode.TreeItemCollapsibleState.None);
        this.description = extension.version;
        this.iconPath = new vscode.ThemeIcon('extensions');
        this.contextValue = 'ms-azuretools.azure-dev.views.extensions.extension';
    }
}

export class ExtensionsTreeDataProvider implements vscode.TreeDataProvider<ExtensionTreeItem> {
    private _onDidChangeTreeData: vscode.EventEmitter<ExtensionTreeItem | undefined | null | void> = new vscode.EventEmitter<ExtensionTreeItem | undefined | null | void>();
    readonly onDidChangeTreeData: vscode.Event<ExtensionTreeItem | undefined | null | void> = this._onDidChangeTreeData.event;

    private readonly extensionProvider = new WorkspaceAzureDevExtensionProvider();

    refresh(): void {
        this._onDidChangeTreeData.fire();
    }

    getTreeItem(element: ExtensionTreeItem): vscode.TreeItem {
        return element;
    }

    async getChildren(element?: ExtensionTreeItem): Promise<ExtensionTreeItem[]> {
        if (element) {
            return [];
        }

        return await callWithTelemetryAndErrorHandling(TelemetryId.WorkspaceViewExtensionResolve, async (context) => {
            const extensions = await this.extensionProvider.getExtensionListResults(context);
            return extensions.map(ext => new ExtensionTreeItem(ext));
        }) ?? [];
    }
}
