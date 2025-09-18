// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

import * as vscode from 'vscode';
import { AzureDevCliWorkspaceResourceProvider } from './AzureDevCliWorkspaceResourceProvider';
import { AzureDevCliWorkspaceResourceBranchDataProvider } from './AzureDevCliWorkspaceResourceBranchDataProvider';
import { WorkspaceAzureDevApplicationProvider } from '../../services/AzureDevApplicationProvider';
import { getAzureResourceExtensionApi, isResourcesExtensionInstalled } from '../../utils/getAzureResourceExtensionApi';

export function scheduleRegisterWorkspaceComponents(extensionContext: vscode.ExtensionContext): void {
    if (isResourcesExtensionInstalled()) {
        // If the Azure Resources extension is already installed, immediately register the workspace components
        void registerWorkspaceComponents(extensionContext);
    } else {
        // If it's not yet installed, set up a listener for extension changes, so that if it becomes installed during this session,
        // we'll register the workspace components without requiring a reload
        const disposable = vscode.extensions.onDidChange(() => {
            if (isResourcesExtensionInstalled()) {
                disposable.dispose();
                void registerWorkspaceComponents(extensionContext);
            }
        });

        // In case it does not get installed in this session, we'll add the disposable to the extension context
        // If it does get installed, the disposable will be disposed twice, which is harmless
        extensionContext.subscriptions.push(disposable);
    }
}

async function registerWorkspaceComponents(extensionContext: vscode.ExtensionContext): Promise<void> {
    const api = await getAzureResourceExtensionApi();

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

    extensionContext.subscriptions.push(...disposables);
}
