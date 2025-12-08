// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

import * as vscode from 'vscode';
import * as path from 'path';

export interface AzureDevApplication {
    configurationPath: vscode.Uri;
    configurationFolder: string,
    workspaceFolder: vscode.WorkspaceFolder;
}

export interface AzureDevApplicationProvider {
    getApplications(): Promise<AzureDevApplication[]>;
}

export class WorkspaceAzureDevApplicationProvider implements AzureDevApplicationProvider {
    async getApplications(): Promise<AzureDevApplication[]> {
        const maxResults = vscode.workspace.getConfiguration('azure-dev').get<number>('maximumAppsToDisplay', 5);
        const files = await vscode.workspace.findFiles('**/azure.{yml,yaml}', '**/node_modules/**', maxResults);

        const applications: AzureDevApplication[] = [];

        for (const file of files) {
            const workspaceFolder = vscode.workspace.getWorkspaceFolder(file);

            if (workspaceFolder) {
                applications.push({
                    configurationPath: file,
                    configurationFolder: path.dirname(file.fsPath),
                    workspaceFolder
                });
            }
        }

        return applications;
    }
}
