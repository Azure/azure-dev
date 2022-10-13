import * as vscode from 'vscode';
import { ResourceModelBase } from "./ResourceGroupsApi";

export interface AzureDevCliModel extends ResourceModelBase {
    getChildren(): AzureDevCliModel[] | Thenable<AzureDevCliModel[]>;
    getTreeItem(): vscode.TreeItem | Thenable<vscode.TreeItem>;
}

export type RefreshHandler = (model: AzureDevCliModel) => void;
