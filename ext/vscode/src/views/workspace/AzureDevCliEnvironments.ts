// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

import * as vscode from 'vscode';
import { callWithTelemetryAndErrorHandling } from '@microsoft/vscode-azext-utils';
import { TelemetryId } from '../../telemetry/telemetryId';
import { AzureDevCliEnvironment } from './AzureDevCliEnvironment';
import { AzureDevCliModel, AzureDevCliModelContext, RefreshHandler } from "./AzureDevCliModel";
import { AzDevEnvListResults, AzureDevEnvListProvider } from '../../services/AzureDevEnvListProvider';
import { AzureDevEnvValuesProvider } from '../../services/AzureDevEnvValuesProvider';

export interface AzureDevCliEnvironmentsModelContext extends AzureDevCliModelContext {
    refreshEnvironments(): void;
}

export class AzureDevCliEnvironments implements AzureDevCliModel {
    constructor(
        context: AzureDevCliModelContext,
        refresh: RefreshHandler,
        private readonly envListProvider: AzureDevEnvListProvider,
        private readonly envValuesProvider: AzureDevEnvValuesProvider,
        private readonly visibleEnvVars: Set<string>,
        private readonly onToggleVisibility: (key: string) => void) {
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
                    environment.DotEnvPath ? vscode.Uri.file(environment.DotEnvPath) : undefined,
                    this.envValuesProvider,
                    this.visibleEnvVars,
                    this.onToggleVisibility));
        }

        return environments;
    }

    getTreeItem(): vscode.TreeItem {
        const treeItem = new vscode.TreeItem(vscode.l10n.t('Environments'), vscode.TreeItemCollapsibleState.Expanded);

        treeItem.contextValue = 'ms-azuretools.azure-dev.views.workspace.environments';

        return treeItem;
    }

    private getResults(): Promise<AzDevEnvListResults> {
        return callWithTelemetryAndErrorHandling(
            TelemetryId.WorkspaceViewEnvironmentResolve,
            async context => {
                return await this.envListProvider.getEnvListResults(context, this.context.configurationFile);
            }
        ) as Promise<AzDevEnvListResults>;
    }
}
