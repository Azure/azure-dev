import * as vscode from 'vscode';
import { AzureDevCliModel } from './AzureDevCliModel';
import { localize } from '../../localize';
import { AzureDevCliEnvironmentsModelContext } from './AzureDevCliEnvironments';

export class AzureDevCliEnvironment implements AzureDevCliModel {
    constructor(
        public readonly context: AzureDevCliEnvironmentsModelContext,
        public readonly name: string,
        private readonly isDefault: boolean,
        public readonly environmentFile: vscode.Uri | undefined) {
    }

    getChildren(): AzureDevCliModel[] {
        return [];
    }

    getTreeItem(): vscode.TreeItem {
        const treeItem = new vscode.TreeItem(this.name);

        treeItem.contextValue = 'ms-azuretools.azure-dev.views.workspace.environment';
        treeItem.iconPath = new vscode.ThemeIcon('cloud');
        treeItem.description = this.isDefault ? localize('views.workspace.AzureDevCliEnvironment.defaultLabel', '(default)') : undefined;

        return treeItem;
    }
}