import * as vscode from 'vscode';
import { ResourceModelBase } from "./ResourceGroupsApi";

export interface AzureDevCliModel extends ResourceModelBase {
    getChildren(): Promise<AzureDevCliModel[]>;
    getTreeItem(): Promise<vscode.TreeItem>;
}
