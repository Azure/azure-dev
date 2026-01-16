// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

import * as vscode from 'vscode';
import { callWithTelemetryAndErrorHandling } from '@microsoft/vscode-azext-utils';
import { TelemetryId } from '../../telemetry/telemetryId';
import { AzureDevEnvValuesProvider } from '../../services/AzureDevEnvValuesProvider';
import { AzureDevCliModel, AzureDevCliModelContext } from './AzureDevCliModel';

export class AzureDevCliEnvironmentVariables implements AzureDevCliModel {
    constructor(
        public readonly context: AzureDevCliModelContext,
        private readonly envValuesProvider: AzureDevEnvValuesProvider,
        private readonly environmentName: string,
        private readonly visibleEnvVars: Set<string>,
        private readonly onToggleVisibility: (key: string) => void
    ) {}

    async getChildren(): Promise<AzureDevCliModel[]> {
        const values = await callWithTelemetryAndErrorHandling(
            TelemetryId.WorkspaceViewEnvironmentResolve,
            async (context) => {
                return await this.envValuesProvider.getEnvValues(context, this.context.configurationFile, this.environmentName);
            }
        ) ?? {};

        return Object.entries(values).map(([key, value]) => {
            return new AzureDevCliEnvironmentVariable(this.context, this.environmentName, key, value, this.visibleEnvVars, this.onToggleVisibility);
        });
    }

    getTreeItem(): vscode.TreeItem {
        const item = new vscode.TreeItem(vscode.l10n.t('Environment Variables'), vscode.TreeItemCollapsibleState.Collapsed);
        item.iconPath = new vscode.ThemeIcon('symbol-variable');
        item.contextValue = 'ms-azuretools.azure-dev.views.workspace.environmentVariables';
        return item;
    }
}

export class AzureDevCliEnvironmentVariable implements AzureDevCliModel {
    constructor(
        public readonly context: AzureDevCliModelContext,
        private readonly environmentName: string,
        private readonly key: string,
        private readonly value: string,
        private readonly visibleEnvVars: Set<string>,
        private readonly onToggleVisibility: (key: string) => void
    ) {}

    getChildren(): AzureDevCliModel[] {
        return [];
    }

    getTreeItem(): vscode.TreeItem {
        const id = `${this.environmentName}/${this.key}`;
        const isVisible = this.visibleEnvVars.has(id);
        const label = isVisible ? `${this.key}=${this.value}` : `${this.key}=Hidden value. Click to view.`;

        const item = new vscode.TreeItem(label);
        item.tooltip = isVisible ? `${this.key}=${this.value}` : 'Click to view value';
        item.iconPath = new vscode.ThemeIcon('key');
        item.contextValue = 'ms-azuretools.azure-dev.views.workspace.environmentVariable';

        item.command = {
            command: 'azure-dev.views.workspace.toggleEnvVarVisibility',
            title: vscode.l10n.t('Toggle Environment Variable Visibility'),
            arguments: [this]
        };

        return item;
    }

    toggleVisibility(): void {
        const id = `${this.environmentName}/${this.key}`;
        this.onToggleVisibility(id);
    }
}
