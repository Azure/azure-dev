// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

import { AzureResourcesApi } from '@microsoft/vscode-azext-utils/hostapi.v2';
import * as vscode from 'vscode';
import { AzureDevCliWorkspaceResourceProvider } from './AzureDevCliWorkspaceResourceProvider';
import { AzureDevCliWorkspaceResourceBranchDataProvider } from './AzureDevCliWorkspaceResourceBranchDataProvider';
import { WorkspaceAzureDevApplicationProvider } from '../../services/AzureDevApplicationProvider';
import { getExtensionExports } from '@microsoft/vscode-azext-utils';
import { AzureExtensionApiProvider } from '@microsoft/vscode-azext-utils/api';

async function getResourceGroupsApi(extensionId: string): Promise<AzureResourcesApi> {
    // TODO: need to get these options in after changes to AzureExtensionApiProvider
    //const rgApiOptions: GetApiOptions = { extensionId };
    const rgApiProvider = await getExtensionExports<AzureExtensionApiProvider>('ms-azuretools.vscode-azureresourcegroups');
    if (rgApiProvider) {
        const v2Api = rgApiProvider.getApi<AzureResourcesApi>('2');

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

    disposables.push(api.resources.registerWorkspaceResourceProvider(workspaceResourceProvider));
    disposables.push(api.resources.registerWorkspaceResourceBranchDataProvider('ms-azuretools.azure-dev.application', new AzureDevCliWorkspaceResourceBranchDataProvider()));

    return disposables;
}
