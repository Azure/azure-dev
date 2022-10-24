import * as vscode from 'vscode';
import { localize } from '../../localize';
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
        const treeItem = new vscode.TreeItem(localize('azure-dev.views.workspace.services.label', 'Services'), vscode.TreeItemCollapsibleState.Expanded);

        treeItem.contextValue = 'ms-azuretools.azure-dev.views.workspace.services';

        return treeItem;
    }
}