import * as vscode from 'vscode';
import { AzureDevCliModel } from "./AzureDevCliModel";

export class AzureDevCliService implements AzureDevCliModel {
    constructor(private readonly name: string) {
    }

    getChildren(): AzureDevCliModel[] {
        return [];
    }

    getTreeItem(): vscode.TreeItem {
        const treeItem = new vscode.TreeItem(this.name);

        treeItem.iconPath = new vscode.ThemeIcon('symbol-interface');

        return treeItem;
    }
}