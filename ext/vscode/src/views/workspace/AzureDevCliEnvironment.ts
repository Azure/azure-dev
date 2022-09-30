import * as vscode from 'vscode';
import { AzureDevCliModel } from "./AzureDevCliModel";

export class AzureDevCliEnvironment implements AzureDevCliModel {
    constructor(private readonly name: string) {
    }

    getChildren(): AzureDevCliModel[] {
        return [];
    }

    getTreeItem(): vscode.TreeItem {
        const treeItem = new vscode.TreeItem(this.name);

        return treeItem;
    }
}