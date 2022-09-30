import { WorkspaceFolder, ProviderResult } from "vscode";
import { ProvideResourceOptions, WorkspaceResource, WorkspaceResourceProvider } from "./ResourceGroupsApi";

export class AzureDevCliWorkspaceResourceProvider implements WorkspaceResourceProvider {
    // TODO: Identify and report changes.
    // onDidChangeResource?: Event<WorkspaceResource | undefined> | undefined;

    // TODO: What if no workspace folder is open?
    getResources(source: WorkspaceFolder, options?: ProvideResourceOptions | undefined): ProviderResult<WorkspaceResource[]> {
        return [];
    }
}