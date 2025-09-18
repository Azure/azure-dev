// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

import { WorkspaceResource, WorkspaceResourceProvider } from '@microsoft/vscode-azureresources-api';
import * as path from 'path';
import * as vscode from 'vscode';
import { AzureDevApplicationProvider, AzureDevApplication } from '../../services/AzureDevApplicationProvider';

export class AzureDevCliWorkspaceResourceProvider extends vscode.Disposable implements WorkspaceResourceProvider {
    private readonly onDidChangeResourceEmitter = new vscode.EventEmitter<WorkspaceResource | undefined>();
    private readonly workspaceFolderSubscription: vscode.Disposable;
    private readonly configFileWatcher: vscode.FileSystemWatcher;
    private readonly configFolderWatchers = new Map<string, vscode.FileSystemWatcher>();

    constructor(private applicationProvider: AzureDevApplicationProvider) {
        super(
            () => {
                this.workspaceFolderSubscription.dispose();
                this.configFileWatcher.dispose();
                this.configFolderWatchers.forEach(watcher => watcher.dispose());
                this.configFolderWatchers.clear();
                this.onDidChangeResourceEmitter.dispose();
            });

        // Listen to workspace folder changes
        this.workspaceFolderSubscription = vscode.workspace.onDidChangeWorkspaceFolders(() => {
            this.onDidChangeResourceEmitter.fire(undefined);
        });

        // Listen to azure.yaml file changes globally
        this.configFileWatcher = vscode.workspace.createFileSystemWatcher(
            '**/azure.{yml,yaml}',
            false, false, false
        );

        const onFileChange = () => {
            this.onDidChangeResourceEmitter.fire(undefined);
        };

        this.configFileWatcher.onDidCreate(onFileChange);
        this.configFileWatcher.onDidChange(onFileChange);
        this.configFileWatcher.onDidDelete(onFileChange);
    }

    readonly onDidChangeResource = this.onDidChangeResourceEmitter.event;

    async getResources(): Promise<WorkspaceResource[]> {
        const applications = await this.applicationProvider.getApplications();
        const resources: WorkspaceResource[] = [];

        this.updateConfigFolderWatchers(applications);

        for (const application of applications) {
            const configurationFilePath = application.configurationPath.fsPath;
            const configurationFolder = application.configurationFolder;
            const configurationFolderName = path.basename(configurationFolder);

            resources.push({
                folder: application.workspaceFolder,
                id: configurationFilePath,
                name: configurationFolderName,
                resourceType: 'ms-azuretools.azure-dev.application'
            });
        }

        return resources;
    }

    private updateConfigFolderWatchers(applications: AzureDevApplication[]): void {
         // Remove any watchers for configuration folders that no longer exist
        for (const [configFolder, watcher] of this.configFolderWatchers) {
            if (applications.findIndex(app => app.configurationFolder === configFolder) === -1) {
                watcher.dispose();
                this.configFolderWatchers.delete(configFolder);
            }
        }

        // Add new watchers for newly added configuration folders
        for (const application of applications) {
            if (this.configFolderWatchers.has(application.configurationFolder)) {
                // already registered
                continue;
            }

            const watcher = vscode.workspace.createFileSystemWatcher(
                application.configurationFolder,
                true, true, false
            );

            watcher.onDidDelete(() => {
                this.onDidChangeResourceEmitter.fire(undefined);
            });

            this.configFolderWatchers.set(application.configurationFolder, watcher);
        }
    }
}
