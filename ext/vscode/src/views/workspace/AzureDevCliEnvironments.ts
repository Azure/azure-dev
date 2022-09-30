import { callWithTelemetryAndErrorHandling } from '@microsoft/vscode-azext-utils';
import * as vscode from 'vscode';
import { localize } from '../../localize';
import { TelemetryId } from '../../telemetry/telemetryId';
import { createAzureDevCli } from '../../utils/azureDevCli';
import { execAsync } from '../../utils/process';
import { AzureDevCliEnvironment } from './AzureDevCliEnvironment';
import { AzureDevCliModel } from "./AzureDevCliModel";

type EnvListResults = {
    Name?: string;
    IsDefault?: boolean;
    DotEnvPath?: string;
}[];

export class AzureDevCliEnvironments implements AzureDevCliModel {
    constructor(private readonly applicationDirectory: string) {
    }

    async getChildren(): Promise<AzureDevCliModel[]> {
        return await callWithTelemetryAndErrorHandling(
            TelemetryId.WorkspaceViewServicesChildren,
            async context => {
                const azureCli = await createAzureDevCli(context);

                const command = azureCli.commandBuilder
                    .withArg('env')
                    .withArg('list')
                    .withNamedArg('--cwd', this.applicationDirectory)
                    .withNamedArg('--output', 'json')
                    .build();

                const envListResultsJson = await execAsync(command);

                const envListResults = JSON.parse(envListResultsJson.stdout) as EnvListResults;

                const environments: AzureDevCliModel[] = [];

                for (const environment of envListResults) {
                    environments.push(new AzureDevCliEnvironment(environment.Name ?? '<unknown>'));
                }

                return environments;
            }) ?? [];
    }

    getTreeItem(): vscode.TreeItem {
        const treeItem = new vscode.TreeItem(localize('azure-dev.views.workspace.environments.label', 'Environments'), vscode.TreeItemCollapsibleState.Expanded);

        return treeItem;
    }
}