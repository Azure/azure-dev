import * as vscode from 'vscode';
import { AzureDevCliModel } from "./AzureDevCliModel";
import { WorkspaceResource } from './ResourceGroupsApi';

export class AzureDevCliApplication implements AzureDevCliModel {
    constructor(private readonly resource: WorkspaceResource) {
    }

    getChildren(): Promise<AzureDevCliModel[]> {
        return Promise.resolve([]);
    }

    getTreeItem(): vscode.TreeItem {
        const treeItem = new vscode.TreeItem(this.resource.name, vscode.TreeItemCollapsibleState.None);

        return treeItem;
    }
}