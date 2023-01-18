// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

import * as path from 'path';
import * as vscode from 'vscode';
import { callWithTelemetryAndErrorHandling } from '@microsoft/vscode-azext-utils';
import { localize } from '../../localize';
import { TelemetryId } from '../../telemetry/telemetryId';
import { createAzureDevCli } from '../../utils/azureDevCli';
import { execAsync } from '../../utils/process';
import { withTimeout } from '../../utils/withTimeout';
import { AzureDevCliEnvironment } from './AzureDevCliEnvironment';
import { AzureDevCliModel, AzureDevCliModelContext, RefreshHandler } from "./AzureDevCliModel";

type EnvListResults = {
    Name?: string;
    IsDefault?: boolean;
    DotEnvPath?: string;
}[];

export interface AzureDevCliEnvironmentsModelContext extends AzureDevCliModelContext {
    refreshEnvironments(): void;
}

export class AzureDevCliEnvironments implements AzureDevCliModel {
    constructor(
        context: AzureDevCliModelContext,
        refresh: RefreshHandler) {
        this.context = {
            ...context,
            refreshEnvironments: () => refresh(this)
        };
    }

    readonly context: AzureDevCliEnvironmentsModelContext;

    async getChildren(): Promise<AzureDevCliModel[]> {
        const envListResults = await this.getResults() ?? [];

        const environments: AzureDevCliModel[] = [];
        
        for (const environment of envListResults) {
            environments.push(
                new AzureDevCliEnvironment(
                    this.context,
                    environment.Name ?? '<unknown>',
                    environment.IsDefault ?? false,
                    environment.DotEnvPath ? vscode.Uri.file(environment.DotEnvPath) : undefined));
        }

        return environments;
    }

    getTreeItem(): vscode.TreeItem {
        const treeItem = new vscode.TreeItem(localize('azure-dev.views.workspace.environments.label', 'Environments'), vscode.TreeItemCollapsibleState.Expanded);

        treeItem.contextValue = 'ms-azuretools.azure-dev.views.workspace.environments';

        return treeItem;
    }

    private async getResults(): Promise<EnvListResults | undefined> {
        return await callWithTelemetryAndErrorHandling(
            TelemetryId.WorkspaceViewEnvironmentResolve,
            async context => {
                const azureCli = await createAzureDevCli(context);

                const configurationFilePath = this.context.configurationFile.fsPath;
                const configurationFileDirectory = path.dirname(configurationFilePath);

                const command = azureCli.commandBuilder
                    .withArg('env')
                    .withArg('list')
                    .withNamedArg('--cwd', configurationFileDirectory)
                    .withNamedArg('--output', 'json')
                    .build();

                const envListResultsJson = await withTimeout(execAsync(command), 30000);

                return JSON.parse(envListResultsJson.stdout) as EnvListResults;
            });
    }
}