import { callWithTelemetryAndErrorHandling } from '@microsoft/vscode-azext-utils';
import * as vscode from 'vscode';
import { localize } from '../../localize';
import { TelemetryId } from '../../telemetry/telemetryId';
import { createAzureDevCli } from '../../utils/azureDevCli';
import { execAsync } from '../../utils/process';
import { AzureDevCliModel } from "./AzureDevCliModel";
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
    constructor(private readonly applicationDirectory: string) {
    }

    async getChildren(): Promise<AzureDevCliModel[]> {
        return await callWithTelemetryAndErrorHandling(
            TelemetryId.WorkspaceViewServicesChildren,
            async context => {
                const azureCli = await createAzureDevCli(context);

                const command = azureCli.commandBuilder
                    .withArg('show')
                    .withNamedArg('--cwd', this.applicationDirectory)
                    .withNamedArg('--output', 'json')
                    .build();

                const showResultsJson = await execAsync(command);

                const showResults = JSON.parse(showResultsJson.stdout) as ShowResults;

                const services: AzureDevCliModel[] = [];

                for (const serviceName in (showResults.services ?? {})) {
                    services.push(new AzureDevCliService(serviceName));
                }

                return services;
            }) ?? [];
    }

    getTreeItem(): vscode.TreeItem {
        const treeItem = new vscode.TreeItem(localize('azure-dev.views.workspace.services.label', 'Services'), vscode.TreeItemCollapsibleState.Expanded);

        return treeItem;
    }
}