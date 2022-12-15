// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

import { getAzureExtensionApi } from '@microsoft/vscode-azext-utils';
import { AzureResourcesApi } from '@microsoft/vscode-azext-utils/hostapi.v2';
import * as vscode from 'vscode';
import { AzureDevCliWorkspaceResourceProvider } from './AzureDevCliWorkspaceResourceProvider';
import { AzureDevCliWorkspaceResourceBranchDataProvider } from './AzureDevCliWorkspaceResourceBranchDataProvider';
import { WorkspaceAzureDevApplicationProvider } from '../../services/AzureDevApplicationProvider';

export async function registerWorkspaceComponents(): Promise<vscode.Disposable[]> {
    const api = await getAzureExtensionApi<AzureResourcesApi>('ms-azuretools.vscode-azureresourcegroups', '2');

    const disposables: vscode.Disposable[] = [];

    // Create and register a workspace resource provider
    const workspaceResourceProvider = new AzureDevCliWorkspaceResourceProvider(new WorkspaceAzureDevApplicationProvider());
    disposables.push(api.resources.registerWorkspaceResourceProvider(workspaceResourceProvider));

    // Create and register a workspace resource branch data provider
    const workspaceResourceBranchDataProvider = new AzureDevCliWorkspaceResourceBranchDataProvider();
    disposables.push(api.resources.registerWorkspaceResourceBranchDataProvider('ms-azuretools.azure-dev.application', workspaceResourceBranchDataProvider));

    // Put the disposables for the providers after the registrations, so they are unregistered before they are disposed
    disposables.push(workspaceResourceProvider);
    disposables.push(workspaceResourceBranchDataProvider);

    return disposables;
}
