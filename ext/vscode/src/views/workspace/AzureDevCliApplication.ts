import * as path from 'path';
import * as vscode from 'vscode';
import { AzureDevCliEnvironments } from './AzureDevCliEnvironments';
import { AzureDevCliModel } from "./AzureDevCliModel";
import { AzureDevCliServices } from './AzureDevCliServices';
import { WorkspaceResource } from './ResourceGroupsApi';

export class AzureDevCliApplication implements AzureDevCliModel {
    constructor(private readonly resource: WorkspaceResource) {
    }

    get configurationFile(): vscode.Uri {
        return vscode.Uri.file(this.resource.id);
    }

    getChildren(): AzureDevCliModel[] {
        const applicationConfigurationPath = this.resource.id;
        const applicationDirectory = path.dirname(applicationConfigurationPath);

        return [
            new AzureDevCliServices(applicationDirectory),
            new AzureDevCliEnvironments(applicationDirectory)
        ];
    }

    getTreeItem(): vscode.TreeItem {
        const treeItem = new vscode.TreeItem(this.resource.name, vscode.TreeItemCollapsibleState.Expanded);

        treeItem.contextValue = 'ms-azuretools.azure-dev.views.workspace.application';
        treeItem.iconPath = new vscode.ThemeIcon('azure');

        return treeItem;
    }
}