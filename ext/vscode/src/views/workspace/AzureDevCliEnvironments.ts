import * as path from 'path';
import * as vscode from 'vscode';
import { callWithTelemetryAndErrorHandling } from '@microsoft/vscode-azext-utils';
import { localize } from '../../localize';
import { TelemetryId } from '../../telemetry/telemetryId';
import { createAzureDevCli } from '../../utils/azureDevCli';
import { execAsync } from '../../utils/process';
import { withTimeout } from '../../utils/withTimeout';
import { AzureDevCliEnvironment } from './AzureDevCliEnvironment';
import { AzureDevCliModel } from "./AzureDevCliModel";

type EnvListResults = {
    Name?: string;
    IsDefault?: boolean;
    DotEnvPath?: string;
}[];

export class AzureDevCliEnvironments implements AzureDevCliModel {
    constructor(
        public readonly configurationFile: vscode.Uri) {
    }

    async getChildren(): Promise<AzureDevCliModel[]> {
        return await callWithTelemetryAndErrorHandling(
            TelemetryId.WorkspaceViewServicesChildren,
            async context => {
                const azureCli = await createAzureDevCli(context);

                const configurationFilePath = this.configurationFile.fsPath;
                const configurationFileDirectory = path.dirname(configurationFilePath);

                const command = azureCli.commandBuilder
                    .withArg('env')
                    .withArg('list')
                    .withNamedArg('--cwd', configurationFileDirectory)
                    .withNamedArg('--output', 'json')
                    .build();

                const envListResultsJson = await withTimeout(execAsync(command), 30000);

                const envListResults = JSON.parse(envListResultsJson.stdout) as EnvListResults;

                const environments: AzureDevCliModel[] = [];

                for (const environment of envListResults) {
                    environments.push(new AzureDevCliEnvironment(environment.Name ?? '<unknown>', environment.IsDefault ?? false));
                }

                return environments;
            }) ?? [];
    }

    getTreeItem(): vscode.TreeItem {
        const treeItem = new vscode.TreeItem(localize('azure-dev.views.workspace.environments.label', 'Environments'), vscode.TreeItemCollapsibleState.Expanded);

        treeItem.contextValue = 'ms-azuretools.azure-dev.views.workspace.environments';

        return treeItem;
    }
}