import * as vscode from 'vscode';
import { mergeMap, Observable } from "rxjs";

export interface AzureDevApplication {
    configurationPath: vscode.Uri;
    workspaceFolder: vscode.WorkspaceFolder;
}

export interface AzureDevApplicationProvider {
    readonly applications: Observable<AzureDevApplication[]>;
}

export class WorkspaceAzureDevApplicationProvider implements AzureDevApplicationProvider {
    constructor() {
        const workspaceFolders =
            new Observable<readonly vscode.WorkspaceFolder[] | undefined>(
                subscriber => {
                    subscriber.next(vscode.workspace.workspaceFolders);

                    const subscription = vscode.workspace.onDidChangeWorkspaceFolders(
                        () => {
                            subscriber.next(vscode.workspace.workspaceFolders);
                        });

                    return () => {
                        subscription.dispose();
                    };
                });

        this.applications =
                workspaceFolders
                    .pipe(
                        mergeMap(WorkspaceAzureDevApplicationProvider.toApplications));
    }

    public readonly applications: Observable<AzureDevApplication[]>;

    private static async toApplications(workspaceFolders: readonly vscode.WorkspaceFolder[] | undefined): Promise<AzureDevApplication[]> {
        const files = await vscode.workspace.findFiles('**/azure.{yml,yaml}', '**/node_modules/**');
        
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
}