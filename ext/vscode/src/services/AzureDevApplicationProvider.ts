// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

import * as vscode from 'vscode';
import { merge, mergeMap, Observable, startWith } from 'rxjs';

export interface AzureDevApplication {
    configurationPath: vscode.Uri;
    workspaceFolder: vscode.WorkspaceFolder;
}

export interface AzureDevApplicationProvider {
    readonly applications: Observable<AzureDevApplication[]>;
}

const azureYamlFilePattern = '**/azure.{yml,yaml}';

async function getApplications(): Promise<AzureDevApplication[]> {
    const files = await vscode.workspace.findFiles(azureYamlFilePattern, '**/node_modules/**');
    
    const applications: AzureDevApplication[] = [];

    for (const file of files) {
        const workspaceFolder = vscode.workspace.getWorkspaceFolder(file);

        if (workspaceFolder) {
            applications.push({
                configurationPath: file,
                workspaceFolder
            });
        }
    }

    return applications;
}

export class WorkspaceAzureDevApplicationProvider implements AzureDevApplicationProvider {
    constructor() {
        const azureYamlWatcher =
            new Observable<void>(
                subscriber => {
                    const watcher = vscode.workspace.createFileSystemWatcher(azureYamlFilePattern, false, false, false);

                    watcher.onDidCreate(uri => subscriber.next());
                    watcher.onDidChange(uri => subscriber.next());
                    watcher.onDidDelete(uri => subscriber.next());

                    return () => watcher.dispose();
                });

        const workspaceFolderWatcher =
            new Observable<void>(
                subscriber => {
                    const subscription = vscode.workspace.onDidChangeWorkspaceFolders(
                        () => {
                            subscriber.next();
                        });

                    return () => {
                        subscription.dispose();
                    };
                });

        this.applications =
            merge(azureYamlWatcher, workspaceFolderWatcher)
                .pipe(
                    startWith(undefined),
                    mergeMap(getApplications));
    }

    public readonly applications: Observable<AzureDevApplication[]>;
}