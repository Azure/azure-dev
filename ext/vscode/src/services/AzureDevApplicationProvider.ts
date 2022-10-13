import * as vscode from 'vscode';
import { interval, mergeMap, Observable, startWith } from "rxjs";

export interface AzureDevApplication {
    configurationPath: vscode.Uri;
}

export interface AzureDevApplicationProvider {
    readonly applications: Observable<AzureDevApplication[]>;
}

export class PollingAzureDevApplicationProvider implements AzureDevApplicationProvider {
    constructor(period: number) {
        this.applications =
            interval(period)
                .pipe(
                    mergeMap(PollingAzureDevApplicationProvider.toApplications),
                    startWith([] as AzureDevApplication[]));
    }

    public readonly applications: Observable<AzureDevApplication[]>;

    private static async toApplications(index: number): Promise<AzureDevApplication[]> {
        const files = await vscode.workspace.findFiles('**/azure.{yml,yaml}', '**/node_modules/**');
        
        const applications: AzureDevApplication[] = [];

        for (const file of files) {
            applications.push({
                configurationPath: file,
            });
        }

        return applications;
    }
}