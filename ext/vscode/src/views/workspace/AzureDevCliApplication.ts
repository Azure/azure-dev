// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

import { WorkspaceResource } from '@microsoft/vscode-azureresources-api';
import * as vscode from 'vscode';
import { callWithTelemetryAndErrorHandling } from '@microsoft/vscode-azext-utils';
import { TelemetryId } from '../../telemetry/telemetryId';
import { AzureDevCliEnvironments } from './AzureDevCliEnvironments';
import { AzureDevCliModel, AzureDevCliModelContext, RefreshHandler } from './AzureDevCliModel';
import { AzureDevCliServices } from './AzureDevCliServices';
import { AzDevShowResults, AzureDevShowProvider } from '../../services/AzureDevShowProvider';
import { AsyncLazy } from '../../utils/lazy';
import { AzureDevEnvListProvider } from '../../services/AzureDevEnvListProvider';

export class AzureDevCliApplication implements AzureDevCliModel {
    private results: AsyncLazy<AzDevShowResults>;

    constructor(
        private readonly resource: WorkspaceResource,
        private readonly refresh: RefreshHandler,
        private readonly showProvider: AzureDevShowProvider,
        private readonly envListProvider: AzureDevEnvListProvider) {
        this.results = new AsyncLazy(() => this.getResults());
    }

    readonly context: AzureDevCliModelContext = {
        configurationFile: vscode.Uri.file(this.resource.id)
    };

    async getChildren(): Promise<AzureDevCliModel[]> {
        const results = await this.results.getValue();

        return [
            new AzureDevCliServices(this.context, Object.keys(results?.services ?? {})),
            new AzureDevCliEnvironments(this.context, this.refresh, this.envListProvider)
        ];
    }

    async getTreeItem(): Promise<vscode.TreeItem> {
        const results = await this.results.getValue();

        const treeItem = new vscode.TreeItem(results?.name ?? this.resource.name, vscode.TreeItemCollapsibleState.Expanded);

        treeItem.contextValue = 'ms-azuretools.azure-dev.views.workspace.application';
        treeItem.iconPath = new vscode.ThemeIcon('azure');

        return treeItem;
    }

    private getResults(): Promise<AzDevShowResults> {
        return callWithTelemetryAndErrorHandling(
            TelemetryId.WorkspaceViewApplicationResolve,
            async actionContext => {
                return await this.showProvider.getShowResults(actionContext, this.context.configurationFile);
            }
        ) as Promise<AzDevShowResults>;
    }
}