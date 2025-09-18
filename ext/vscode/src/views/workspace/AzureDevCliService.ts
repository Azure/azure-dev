// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

import * as vscode from 'vscode';
import { AzureDevCliModel, AzureDevCliModelContext } from "./AzureDevCliModel";

export class AzureDevCliService implements AzureDevCliModel {
    constructor(
        public readonly context: AzureDevCliModelContext,
        public readonly name: string) {
    }

    getChildren(): AzureDevCliModel[] {
        return [];
    }

    getTreeItem(): vscode.TreeItem {
        const treeItem = new vscode.TreeItem(this.name);

        treeItem.contextValue = 'ms-azuretools.azure-dev.views.workspace.service';
        treeItem.iconPath = new vscode.ThemeIcon('symbol-interface');

        return treeItem;
    }
}