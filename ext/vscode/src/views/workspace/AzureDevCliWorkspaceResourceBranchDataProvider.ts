import * as vscode from 'vscode';
import { ProviderResult, TreeItem } from "vscode";
import { AzureDevCliApplication } from "./AzureDevCliApplication";
import { AzureDevCliModel } from "./AzureDevCliModel";
import { BranchDataProvider, WorkspaceResource } from "./ResourceGroupsApi";

export class AzureDevCliWorkspaceResourceBranchDataProvider extends vscode.Disposable implements BranchDataProvider<WorkspaceResource, AzureDevCliModel> {
    private readonly onDidChangeTreeDataEmitter = new vscode.EventEmitter<void | AzureDevCliModel | null | undefined>();

    constructor() {
        super(
            () => {
                this.onDidChangeTreeDataEmitter.dispose();
            });
    }

    getChildren(element: AzureDevCliModel): ProviderResult<AzureDevCliModel[]> {
        return element.getChildren();
    }

    getResourceItem(element: WorkspaceResource): AzureDevCliModel | Thenable<AzureDevCliModel> {
        return new AzureDevCliApplication(element, model => this.onDidChangeTreeDataEmitter.fire(model));
    }

    createResourceItem?: (() => ProviderResult<WorkspaceResource>) | undefined;

    readonly onDidChangeTreeData = this.onDidChangeTreeDataEmitter.event;

    getTreeItem(element: AzureDevCliModel): TreeItem | Thenable<TreeItem> {
        return element.getTreeItem();
    }
}