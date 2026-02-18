// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

import * as vscode from 'vscode';
import * as path from 'path';
import { AzureDevCliModel } from '../workspace/AzureDevCliModel';
import { AzureDevApplicationProvider, WorkspaceAzureDevApplicationProvider } from '../../services/AzureDevApplicationProvider';
import { AzureDevCliApplication } from '../workspace/AzureDevCliApplication';
import { WorkspaceAzureDevShowProvider } from '../../services/AzureDevShowProvider';
import { WorkspaceAzureDevEnvListProvider } from '../../services/AzureDevEnvListProvider';
import { WorkspaceAzureDevEnvValuesProvider } from '../../services/AzureDevEnvValuesProvider';
import { WorkspaceResource } from '@microsoft/vscode-azureresources-api';
import { FileSystemWatcherService } from '../../services/FileSystemWatcherService';

export class MyProjectTreeDataProvider implements vscode.TreeDataProvider<AzureDevCliModel> {
    private _onDidChangeTreeData: vscode.EventEmitter<AzureDevCliModel | undefined | null | void> = new vscode.EventEmitter<AzureDevCliModel | undefined | null | void>();
    readonly onDidChangeTreeData: vscode.Event<AzureDevCliModel | undefined | null | void> = this._onDidChangeTreeData.event;

    private readonly applicationProvider: AzureDevApplicationProvider;
    private readonly showProvider = new WorkspaceAzureDevShowProvider();
    private readonly envListProvider = new WorkspaceAzureDevEnvListProvider();
    private readonly envValuesProvider = new WorkspaceAzureDevEnvValuesProvider();
    private readonly configFileWatcherDisposable: vscode.Disposable;

    constructor(private fileSystemWatcherService: FileSystemWatcherService) {
        this.applicationProvider = new WorkspaceAzureDevApplicationProvider();

        // Listen to azure.yaml file changes globally
        const onFileChange = () => {
            this.refresh();
        };

        this.configFileWatcherDisposable = this.fileSystemWatcherService.watch(
            '**/azure.{yml,yaml}',
            onFileChange
        );
    }

    refresh(): void {
        this._onDidChangeTreeData.fire();
    }

    getTreeItem(element: AzureDevCliModel): vscode.TreeItem | Thenable<vscode.TreeItem> {
        return element.getTreeItem();
    }

    async getChildren(element?: AzureDevCliModel): Promise<AzureDevCliModel[]> {
        if (element) {
            return element.getChildren();
        }

        const applications = await this.applicationProvider.getApplications();
        const children: AzureDevCliModel[] = [];

        for (const application of applications) {
            const configurationFilePath = application.configurationPath.fsPath;
            const configurationFolder = application.configurationFolder;
            const configurationFolderName = path.basename(configurationFolder);

            const workspaceResource: WorkspaceResource = {
                folder: application.workspaceFolder,
                id: configurationFilePath,
                name: configurationFolderName,
                resourceType: 'ms-azuretools.azure-dev.application'
            };

            const appModel = new AzureDevCliApplication(
                    workspaceResource,
                    (model: AzureDevCliModel) => this._onDidChangeTreeData.fire(model),
                    this.showProvider,
                    this.envListProvider,
                    this.envValuesProvider,
                    new Set<string>(),
                    () => { /* no-op */},
                    false // Do not include environments
                );

            children.push(appModel);
        }

        return children;
    }

    dispose(): void {
        this.configFileWatcherDisposable.dispose();
        this._onDidChangeTreeData.dispose();
    }
}
