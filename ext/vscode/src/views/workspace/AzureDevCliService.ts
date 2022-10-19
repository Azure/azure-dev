import * as vscode from 'vscode';
import { AzureDevCliModel, AzureDevCliModelContext } from "./AzureDevCliModel";

export class AzureDevCliService implements AzureDevCliModel {
    constructor(
        public readonly context: AzureDevCliModelContext,
        private readonly name: string) {
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