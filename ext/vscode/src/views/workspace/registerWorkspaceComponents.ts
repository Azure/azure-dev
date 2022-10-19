import * as vscode from 'vscode';
import { AzureDevCliWorkspaceResourceProvider } from './AzureDevCliWorkspaceResourceProvider';
import { AzureDevCliWorkspaceResourceBranchDataProvider } from './AzureDevCliWorkspaceResourceBranchDataProvider';
import { AzureResourcesApiManager, GetApiOptions, V2AzureResourcesApi } from './ResourceGroupsApi';
import { WorkspaceAzureDevApplicationProvider } from '../../services/AzureDevApplicationProvider';

export async function getApiExport<T>(extensionId: string): Promise<T | undefined> {
    const extension: vscode.Extension<T> | undefined = vscode.extensions.getExtension(extensionId);
    if (extension) {
        if (!extension.isActive) {
            await extension.activate();
        }

        return extension.exports;
    }

    return undefined;
}

async function getResourceGroupsApi(extensionId: string): Promise<V2AzureResourcesApi> {
    const rgApiOptions: GetApiOptions = { extensionId };
    const rgApiProvider = await getApiExport<AzureResourcesApiManager>('ms-azuretools.vscode-azureresourcegroups');
    if (rgApiProvider) {
        const v2Api = rgApiProvider.getApi<V2AzureResourcesApi>('2', rgApiOptions);

        if (v2Api === undefined) {
            throw new Error('Could not find the V2 Azure Resource Groups API.');
        }

        return v2Api;
    } else {
        throw new Error('Could not find the Azure Resource Groups extension');
    }
}

export async function registerWorkspaceComponents(extensionId: string): Promise<vscode.Disposable[]> {
    const api = await getResourceGroupsApi(extensionId);

    const disposables: vscode.Disposable[] = [];

    const workspaceResourceProvider = new AzureDevCliWorkspaceResourceProvider(new WorkspaceAzureDevApplicationProvider());

    disposables.push(workspaceResourceProvider);

    disposables.push(api.registerWorkspaceResourceProvider(workspaceResourceProvider));
    disposables.push(api.registerWorkspaceResourceBranchDataProvider('ms-azuretools.azure-dev.application', new AzureDevCliWorkspaceResourceBranchDataProvider()));

    return disposables;
}
