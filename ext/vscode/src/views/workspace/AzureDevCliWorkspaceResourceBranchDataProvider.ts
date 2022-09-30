import { ProviderResult, Event, TreeItem } from "vscode";
import { AzureDevCliApplication } from "./AzureDevCliApplication";
import { AzureDevCliModel } from "./AzureDevCliModel";
import { BranchDataProvider, WorkspaceResource } from "./ResourceGroupsApi";

// TODO: Add helper interface for workspace branch data provider?
export class AzureDevCliWorkspaceResourceBranchDataProvider implements BranchDataProvider<WorkspaceResource, AzureDevCliModel> {
    getChildren(element: AzureDevCliModel): ProviderResult<AzureDevCliModel[]> {
        return element.getChildren();
    }

    getResourceItem(element: WorkspaceResource): AzureDevCliModel | Thenable<AzureDevCliModel> {
        return new AzureDevCliApplication(element);
    }

    createResourceItem?: (() => ProviderResult<WorkspaceResource>) | undefined;

    onDidChangeTreeData?: Event<void | AzureDevCliModel | null | undefined> | undefined;

    getTreeItem(element: AzureDevCliModel): TreeItem | Thenable<TreeItem> {
        return element.getTreeItem();
    }
}