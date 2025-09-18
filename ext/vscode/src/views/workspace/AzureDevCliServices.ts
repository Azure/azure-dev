// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

import * as vscode from 'vscode';
import { AzureDevCliModel, AzureDevCliModelContext } from './AzureDevCliModel';
import { AzureDevCliService } from './AzureDevCliService';

export class AzureDevCliServices implements AzureDevCliModel {
    constructor(
        public readonly context: AzureDevCliModelContext,
        private readonly serviceNames: string[]) {
    }

    getChildren(): AzureDevCliModel[] {
        return this.serviceNames.map((serviceName) => new AzureDevCliService(this.context, serviceName));
    }

    getTreeItem(): vscode.TreeItem {
        const treeItem = new vscode.TreeItem(vscode.l10n.t('Services'), vscode.TreeItemCollapsibleState.Expanded);

        treeItem.contextValue = 'ms-azuretools.azure-dev.views.workspace.services';

        return treeItem;
    }
}