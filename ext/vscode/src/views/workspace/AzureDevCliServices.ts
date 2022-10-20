import * as path from 'path';
import * as vscode from 'vscode';
import { callWithTelemetryAndErrorHandling } from '@microsoft/vscode-azext-utils';
import { localize } from '../../localize';
import { TelemetryId } from '../../telemetry/telemetryId';
import { createAzureDevCli } from '../../utils/azureDevCli';
import { execAsync } from '../../utils/process';
import { withTimeout } from '../../utils/withTimeout';
import { AzureDevCliModel, AzureDevCliModelContext } from './AzureDevCliModel';
import { AzureDevCliService } from './AzureDevCliService';

interface ShowResults {
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

export class AzureDevCliServices implements AzureDevCliModel {
    constructor(public readonly context: AzureDevCliModelContext) {
    }

    async getChildren(): Promise<AzureDevCliModel[]> {
        return await callWithTelemetryAndErrorHandling(
            TelemetryId.WorkspaceViewServicesChildren,
            async context => {
                const azureCli = await createAzureDevCli(context);

                const configurationFilePath = this.context.configurationFile.fsPath;
                const configurationFileDirectory = path.dirname(configurationFilePath);

                const command = azureCli.commandBuilder
                    .withArg('show')
                    .withNamedArg('--cwd', configurationFileDirectory)
                    .withNamedArg('--output', 'json')
                    .build();

                const showResultsJson = await withTimeout(execAsync(command), 30000);

                const showResults = JSON.parse(showResultsJson.stdout) as ShowResults;

                const services: AzureDevCliModel[] = [];

                for (const serviceName in (showResults.services ?? {})) {
                    services.push(new AzureDevCliService(this.context, serviceName));
                }

                return services;
            }) ?? [];
    }

    getTreeItem(): vscode.TreeItem {
        const treeItem = new vscode.TreeItem(localize('azure-dev.views.workspace.services.label', 'Services'), vscode.TreeItemCollapsibleState.Expanded);

        treeItem.contextValue = 'ms-azuretools.azure-dev.views.workspace.services';

        return treeItem;
    }
}