import * as vscode from 'vscode';
import { AzureDevCliModel } from "./AzureDevCliModel";
import { localize } from '../../localize';

export class AzureDevCliEnvironment implements AzureDevCliModel {
    constructor(
        private readonly name: string,
        private readonly isDefault: boolean) {
    }

    getChildren(): AzureDevCliModel[] {
        return [];
    }

    getTreeItem(): vscode.TreeItem {
        const treeItem = new vscode.TreeItem(this.name);

        treeItem.iconPath = new vscode.ThemeIcon('cloud');
        treeItem.description = this.isDefault ? localize('views.workspace.AzureDevCliEnvironment.defaultLabel', '(default)') : undefined;

        return treeItem;
    }
}