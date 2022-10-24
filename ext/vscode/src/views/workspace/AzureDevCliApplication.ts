import * as path from 'path';
import * as vscode from 'vscode';
import { callWithTelemetryAndErrorHandling } from '@microsoft/vscode-azext-utils';
import { TelemetryId } from '../../telemetry/telemetryId';
import { createAzureDevCli } from '../../utils/azureDevCli';
import { execAsync } from '../../utils/process';
import { withTimeout } from '../../utils/withTimeout';
import { AzureDevCliEnvironments } from './AzureDevCliEnvironments';
import { AzureDevCliModel, AzureDevCliModelContext, RefreshHandler } from './AzureDevCliModel';
import { AzureDevCliServices } from './AzureDevCliServices';
import { WorkspaceResource } from './ResourceGroupsApi';

interface ShowResults {
    name?: string;
    services?: {
        [name: string]: {
            project?: {
                path?: string;
                language?: string;
            }
            target?: {
                resourceIds?: string[];
            }
        }
    }
}

export class AzureDevCliApplication implements AzureDevCliModel {
    private results: ShowResults | undefined;

    constructor(
        private readonly resource: WorkspaceResource,
        private readonly refresh: RefreshHandler) {
    }

    readonly context: AzureDevCliModelContext = {
        configurationFile: vscode.Uri.file(this.resource.id)
    };

    async getChildren(): Promise<AzureDevCliModel[]> {
        const results = await this.getResults();

        return [
            new AzureDevCliServices(this.context, Object.keys(results?.services ?? {})),
            new AzureDevCliEnvironments(this.context, this.refresh)
        ];
    }

    async getTreeItem(): Promise<vscode.TreeItem> {
        const results = await this.getResults();
        
        const treeItem = new vscode.TreeItem(results?.name ?? this.resource.name, vscode.TreeItemCollapsibleState.Expanded);

        treeItem.contextValue = 'ms-azuretools.azure-dev.views.workspace.application';
        treeItem.iconPath = new vscode.ThemeIcon('azure');

        return treeItem;
    }

    private getResults(): Promise<ShowResults | undefined> {
        return callWithTelemetryAndErrorHandling(
            TelemetryId.WorkspaceViewApplicationResolve,
            async context => {
                if (!this.results) {
                    const azureCli = await createAzureDevCli(context);

                    const configurationFilePath = this.context.configurationFile.fsPath;
                    const configurationFileDirectory = path.dirname(configurationFilePath);

                    const command = azureCli.commandBuilder
                        .withArg('show')
                        .withNamedArg('--cwd', configurationFileDirectory)
                        .withNamedArg('--output', 'json')
                        .build();

                    const showResultsJson = await withTimeout(execAsync(command), 30000);

                    this.results = JSON.parse(showResultsJson.stdout) as ShowResults;
                }

                return this.results;
            });
    }
}