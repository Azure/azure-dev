// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

import { BranchDataProvider, WorkspaceResource } from '@microsoft/vscode-azureresources-api';
import * as vscode from 'vscode';
import { ProviderResult, TreeItem } from 'vscode';
import { AzureDevEnvListProvider, WorkspaceAzureDevEnvListProvider } from '../../services/AzureDevEnvListProvider';
import { AzureDevShowProvider, WorkspaceAzureDevShowProvider } from '../../services/AzureDevShowProvider';
import { AzureDevEnvValuesProvider, WorkspaceAzureDevEnvValuesProvider } from '../../services/AzureDevEnvValuesProvider';
import { AzureDevCliApplication } from './AzureDevCliApplication';
import { AzureDevCliModel } from './AzureDevCliModel';

export class AzureDevCliWorkspaceResourceBranchDataProvider extends vscode.Disposable implements BranchDataProvider<WorkspaceResource, AzureDevCliModel> {
    private readonly onDidChangeTreeDataEmitter = new vscode.EventEmitter<void | AzureDevCliModel | null | undefined>();
    private readonly visibleEnvVars = new Set<string>();

    constructor(
        private readonly showProvider: AzureDevShowProvider = new WorkspaceAzureDevShowProvider(),
        private readonly envListProvider: AzureDevEnvListProvider = new WorkspaceAzureDevEnvListProvider(),
        private readonly envValuesProvider: AzureDevEnvValuesProvider = new WorkspaceAzureDevEnvValuesProvider()
    ) {
        super(
            () => {
                this.onDidChangeTreeDataEmitter.dispose();
            });
    }

    getChildren(element: AzureDevCliModel): ProviderResult<AzureDevCliModel[]> {
        return element.getChildren();
    }

    getResourceItem(element: WorkspaceResource): AzureDevCliModel | Thenable<AzureDevCliModel> {
        return new AzureDevCliApplication(
            element,
            model => this.onDidChangeTreeDataEmitter.fire(model),
            this.showProvider,
            this.envListProvider,
            this.envValuesProvider,
            this.visibleEnvVars,
            (key) => this.toggleVisibility(key)
        );
    }

    toggleVisibility(key: string): void {
        if (this.visibleEnvVars.has(key)) {
            this.visibleEnvVars.delete(key);
        } else {
            this.visibleEnvVars.add(key);
        }
        this.onDidChangeTreeDataEmitter.fire();
    }

    createResourceItem?: (() => ProviderResult<WorkspaceResource>) | undefined;

    readonly onDidChangeTreeData = this.onDidChangeTreeDataEmitter.event;

    getTreeItem(element: AzureDevCliModel): TreeItem | Thenable<TreeItem> {
        return element.getTreeItem();
    }
}
